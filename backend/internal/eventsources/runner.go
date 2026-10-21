package eventsources

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

var (
	ErrSyncRunning    = errors.New("event source sync is already running")
	ErrSourceDisabled = errors.New("event source is disabled")
	ErrSourceInvalid  = errors.New("event source configuration is invalid")
)

// Runner executes the same bounded Generic import for scheduled and manual runs.
type Runner struct {
	db       *store.Pool
	sources  *sourceconfig.Repository
	store    providers.SyncStore
	cityID   uuid.UUID
	cities   sourceconfig.CityBindings
	logger   *slog.Logger
	importFn func(context.Context, uuid.UUID, sourceconfig.Source, string, providers.SyncStore) (providers.ImportStats, error)
}

func NewRunner(db *store.Pool, sources *sourceconfig.Repository, cityID uuid.UUID) (*Runner, error) {
	if db == nil || sources == nil || cityID == uuid.Nil {
		return nil, errors.New("event source runner dependencies are required")
	}
	bindings, err := sourceconfig.LoadCityBindings()
	if err != nil {
		return nil, err
	}
	return &Runner{db: db, sources: sources, store: providers.NewRepository(db), cityID: cityID, cities: bindings, logger: slog.Default(), importFn: importRecords}, nil
}

// CityForSource is shared by manual execution and the scheduled source filter.
// An unbound source retains the caller's legacy default (Moscow in production).
func (runner *Runner) CityForSource(sourceKey string) uuid.UUID {
	if city, ok := runner.cities[canonicalSourceKey(sourceKey)]; ok {
		return city
	}
	return runner.cityID
}

func (runner *Runner) prepare(ctx context.Context, sourceID uuid.UUID) (sourceconfig.Source, string, error) {
	source, secret, err := runner.sources.GetForExecution(ctx, sourceID)
	if err != nil {
		return sourceconfig.Source{}, "", err
	}
	if !source.Enabled {
		return sourceconfig.Source{}, "", ErrSourceDisabled
	}
	if err := runner.sources.ValidateExecutionIdentity(ctx, source); err != nil {
		return sourceconfig.Source{}, "", err
	}
	source.SourceKey = canonicalSourceKey(source.SourceKey)
	if _, err := configured(source, secret); err != nil {
		return sourceconfig.Source{}, "", ErrSourceInvalid
	}
	if city, bound := runner.cities[source.SourceKey]; bound {
		var timezone string
		if err := runner.db.QueryRow(ctx, "SELECT timezone FROM cities WHERE id=$1", city).Scan(&timezone); err != nil || strings.TrimSpace(timezone) != strings.TrimSpace(source.Defaults.Timezone) {
			return sourceconfig.Source{}, "", ErrSourceInvalid
		}
	}
	return source, secret, nil
}

// Run executes a source synchronously. ErrSyncRunning is returned if a manual
// or another scheduler instance already owns the per-source PostgreSQL lock.
func (runner *Runner) Run(ctx context.Context, sourceID uuid.UUID) (providers.ImportStats, error) {
	syncCtx, cancel := context.WithTimeout(ctx, generic.FullSyncTimeout)
	defer cancel()
	source, secret, err := runner.prepare(syncCtx, sourceID)
	if err != nil {
		return providers.ImportStats{}, err
	}
	lock, err := acquireSourceSyncLock(syncCtx, runner.db, source.SourceKey)
	if err != nil {
		return providers.ImportStats{}, err
	}
	defer lock.Release()
	return runner.importFn(syncCtx, runner.CityForSource(source.SourceKey), source, secret, runner.store)
}

// Start reserves the lock before returning so the HTTP handler can preserve its
// immediate 202/409 contract. The long-running import uses the supplied app
// context rather than the request context.
func (runner *Runner) Start(ctx context.Context, sourceID uuid.UUID) error {
	reservationCtx, cancelReservation := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReservation()
	source, secret, err := runner.prepare(reservationCtx, sourceID)
	if err != nil {
		return err
	}
	lock, err := acquireSourceSyncLock(reservationCtx, runner.db, source.SourceKey)
	if err != nil {
		return err
	}
	go func() {
		defer lock.Release()
		syncCtx, cancel := context.WithTimeout(ctx, generic.FullSyncTimeout)
		defer cancel()
		started := time.Now()
		stats, runErr := runner.importFn(syncCtx, runner.CityForSource(source.SourceKey), source, secret, runner.store)
		attributes := []any{"provider", source.SourceKey, "sync_run_id", stats.SyncRunID, "duration", time.Since(started), "pages_fetched", stats.PagesFetched, "fetched", stats.Fetched, "inserted", stats.Inserted, "updated", stats.Updated, "skipped", stats.Skipped, "errors", stats.Errors}
		if runErr != nil || stats.Errors > 0 {
			runner.logger.Error("generic event source sync finished", append(attributes, "final_status", providers.SyncRunFailed, "error", "provider import failed")...)
			return
		}
		runner.logger.Info("generic event source sync finished", append(attributes, "final_status", providers.SyncRunSucceeded)...)
	}()
	return nil
}

type sourceSyncLock struct {
	conn      *pgx.Conn
	sourceKey string
}

func acquireSourceSyncLock(ctx context.Context, db *store.Pool, sourceKey string) (*sourceSyncLock, error) {
	sourceKey = canonicalSourceKey(sourceKey)
	conn, err := pgx.ConnectConfig(ctx, db.Config().ConnConfig.Copy())
	if err != nil {
		return nil, errors.New("event source sync lock unavailable")
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, sourceKey).Scan(&acquired); err != nil {
		_ = conn.Close(ctx)
		return nil, errors.New("event source sync lock unavailable")
	}
	if !acquired {
		_ = conn.Close(ctx)
		return nil, ErrSyncRunning
	}
	return &sourceSyncLock{conn: conn, sourceKey: sourceKey}, nil
}

func (lock *sourceSyncLock) Release() {
	if lock == nil || lock.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released bool
	if err := lock.conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lock.sourceKey).Scan(&released); err != nil || !released {
		_ = lock.conn.Close(ctx)
		return
	}
	_ = lock.conn.Close(ctx)
}
