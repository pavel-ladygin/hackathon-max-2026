// Command event-sync periodically imports external events into PostgreSQL.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

const (
	kudaGoPriority  = 100
	timepadPriority = 50
)

var errPartialProviderSync = errors.New("provider sync completed with record errors")
var errProviderSyncBusy = errors.New("provider sync skipped because another worker owns the lock")

type eventImporter interface {
	Import(context.Context, uuid.UUID, providers.SyncStore, func(error)) (providers.ImportStats, error)
}

type syncProvider struct {
	name        string
	priority    int
	importer    eventImporter
	acquireLock func(context.Context) (func() error, bool, error)
}

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("event sync stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open event sync database: %w", err)
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return errors.New("event sync requires current migrations; run cmd/migrate first")
	}
	var sourceRepository *sourceconfig.Repository
	if cfg.EventSyncProvider == "" {
		secretCodec, err := sourceconfig.NewSecretCodec(cfg.InviteEncryptionKey, cfg.InviteEncryptionKeyVersion)
		if err != nil {
			logger.Warn("generic event sources disabled", "reason", "provider secret encryption key is not configured")
		} else {
			sourceRepository, err = sourceconfig.NewRepository(db, secretCodec)
			if err != nil {
				return err
			}
			if err := sourceRepository.CheckSecrets(ctx); err != nil {
				return err
			}
		}
	}

	cityID := cfg.EventSyncCityID
	var cityName, cityTimezone string
	if err := db.QueryRow(ctx, "SELECT name,timezone FROM cities WHERE id = $1", cityID).Scan(&cityName, &cityTimezone); err != nil {
		return errors.New("configured catalog city is missing or unavailable; initialize it first")
	}
	if err := validateSyncCity(cfg, cityName, cityTimezone); err != nil {
		return err
	}

	lockProvider := func(provider string) func(context.Context) (func() error, bool, error) {
		return func(lockCtx context.Context) (func() error, bool, error) {
			lock, acquired, err := providers.TryAcquireProviderSyncLock(lockCtx, db, provider)
			if lock == nil {
				return nil, acquired, err
			}
			return lock.Release, acquired, err
		}
	}
	// Preserve the existing provider-wide Timepad lock/rate-limit coordination.
	// It conservatively serializes cities; run audit/reconciliation is city-scoped.
	syncProviders := []syncProvider{}
	if includesProvider(cfg.EventSyncProvider, "kudago") {
		kudaGoClient, err := kudago.NewClient(kudago.Options{
			BaseURL:              cfg.KudaGoBaseURL,
			Timeout:              cfg.KudaGoTimeout,
			Location:             cfg.KudaGoLocation,
			PageSize:             cfg.KudaGoPageSize,
			CityID:               cityID,
			Timezone:             cityTimezone,
			MaxRequestsPerMinute: cfg.KudaGoMaxRequestsPerMinute,
		})
		if err != nil {
			return err
		}
		syncProviders = append(syncProviders, syncProvider{name: "kudago", priority: kudaGoPriority, importer: kudaGoClient, acquireLock: lockProvider("kudago")})
	}
	if includesProvider(cfg.EventSyncProvider, "timepad") {
		timepadCity := cfg.TimepadCityName
		if cityID.String() == catalogseed.MoscowCityID && timepadCity == "" {
			timepadCity = "Москва"
		}
		if cfg.TimepadToken == "" || timepadCity == "" {
			if cfg.EventSyncProvider == "timepad" {
				return errors.New("Timepad-only worker requires TIMEPAD_TOKEN and TIMEPAD_CITY_NAME")
			}
			logger.Warn("event sync provider disabled", "provider", "timepad", "reason", "token or explicit provider city is not configured")
		} else {
			timepadClient, err := timepad.NewClient(timepad.Options{
				BaseURL: cfg.TimepadBaseURL, Token: cfg.TimepadToken,
				Timeout: cfg.TimepadTimeout, PageSize: cfg.TimepadPageSize,
				MaxRequestsPerMinute: cfg.TimepadMaxRequestsPerMinute,
				UpsertOnly:           cfg.TimepadUpsertOnly,
				CityID:               cityID, CityName: timepadCity, Timezone: cityTimezone,
			})
			if err != nil {
				return err
			}
			syncProviders = append(syncProviders, syncProvider{
				name: "timepad", priority: timepadPriority, importer: timepadClient,
				acquireLock: lockProvider("timepad"),
			})
		}
	}

	repository := providers.NewRepository(db)
	var genericRunner *eventsources.Runner
	if sourceRepository != nil {
		genericRunner, err = eventsources.NewRunner(db, sourceRepository, uuid.MustParse(catalogseed.MoscowCityID))
		if err != nil {
			return err
		}
	}
	logger.Info("event sync starting", "providers", len(syncProviders), "provider_selection", cfg.EventSyncProvider, "timepad_upsert_only", cfg.TimepadUpsertOnly, "city_id", cityID, "city", cityName, "timezone", cityTimezone, "interval", cfg.EventSyncInterval, "run_on_start", cfg.EventSyncRunOnStart)
	runSyncLoop(ctx, cfg.EventSyncInterval, cfg.EventSyncRunOnStart, func() bool {
		cycleProviders := append([]syncProvider(nil), syncProviders...)
		if sourceRepository != nil {
			genericSources, err := sourceRepository.List(ctx)
			if err != nil {
				logger.Error("generic event source list unavailable", "error", "could not load source configuration")
			} else {
				cycleProviders = append(cycleProviders, genericSyncProviders(genericSources, genericRunner, cityID)...)
			}
		}
		if err := syncProvidersOnce(ctx, cityID, repository, cycleProviders, logger); err != nil {
			return false
		}
		return true
	})
	logger.Info("event sync shutdown complete")
	return nil
}

func validateSyncCity(cfg config.Config, name, timezone string) error {
	return cfg.ValidateSyncCity(name, timezone)
}

func runSyncLoop(ctx context.Context, interval time.Duration, runOnStart bool, syncAll func() bool) {
	if runOnStart && !syncAll() {
		return
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			if !syncAll() {
				return
			}
		}
	}
}

func syncProvidersOnce(ctx context.Context, cityID uuid.UUID, repository providers.SyncStore, syncProviders []syncProvider, logger *slog.Logger) error {
	for _, provider := range syncProviders {
		if err := syncOne(ctx, cityID, repository, provider, logger); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func syncOne(ctx context.Context, cityID uuid.UUID, repository providers.SyncStore, provider syncProvider, logger *slog.Logger) error {
	startedAt := time.Now()
	if provider.acquireLock != nil {
		release, acquired, err := provider.acquireLock(ctx)
		if err != nil {
			logger.Error("provider sync coordination unavailable", "provider", provider.name)
			return fmt.Errorf("acquire %s sync lock: %w", provider.name, err)
		}
		if !acquired {
			logger.Info("provider_sync_skipped_already_running", "provider", provider.name, "city_id", cityID, "final_status", "skipped")
			return errProviderSyncBusy
		}
		defer func() {
			if err := release(); err != nil {
				logger.Error("provider sync lock release failed", "provider", provider.name)
			}
		}()
	}
	stats, syncErr := provider.importer.Import(ctx, cityID, repository, func(err error) {
		stage := providers.SyncErrorStage(err)
		if stage == "" {
			stage = "persist_record"
			err = providers.WithSyncStage(err, stage)
		}
		attrs := []any{"provider", provider.name, "city_id", cityID, "stage", stage, "error_code", providers.SyncErrorCode(err)}
		switch stage {
		case "poster_resolve", "poster_fetch", "poster_persist":
			logger.Warn("event sync poster recovery failed", attrs...)
		case "finalize_sync":
			logger.Error("event sync finalization failed", attrs...)
		default:
			logger.Error("event sync record failed", attrs...)
		}
	})
	if errors.Is(syncErr, eventsources.ErrSyncRunning) || errors.Is(syncErr, eventsources.ErrSourceDisabled) {
		logger.Info("event sync provider skipped", "provider", provider.name, "priority", provider.priority, "reason", "source sync is already running or disabled")
		return nil
	}
	syncRunID := ""
	if stats.SyncRunID != uuid.Nil {
		syncRunID = stats.SyncRunID.String()
	}
	finalStatus := string(providers.SyncRunSucceeded)
	finalErr := syncErr
	if errors.Is(syncErr, context.Canceled) {
		finalStatus = string(providers.SyncRunCancelled)
	} else if syncErr != nil {
		finalStatus = string(providers.SyncRunFailed)
	} else if stats.Errors > 0 {
		finalStatus = string(providers.SyncRunFailed)
		finalErr = errPartialProviderSync
	}
	attributes := []any{
		"provider", provider.name,
		"city_id", cityID,
		"poster_errors", stats.PosterErrors,
		"sync_run_id", syncRunID,
		"priority", provider.priority,
		"duration", time.Since(startedAt),
		"pages_fetched", stats.PagesFetched,
		"fetched", stats.Fetched,
		"matched", stats.Matched,
		"normalized", stats.Normalized,
		"inserted", stats.Inserted,
		"updated", stats.Updated,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"rejected", stats.Rejections.Total(),
		"reject_city", stats.Rejections.City,
		"reject_invalid_id", stats.Rejections.InvalidID,
		"reject_missing_title", stats.Rejections.MissingTitle,
		"reject_missing_starts_at", stats.Rejections.MissingStartsAt,
		"reject_invalid_starts_at", stats.Rejections.InvalidStartsAt,
		"reject_malformed_categories", stats.Rejections.MalformedCategories,
		"rejected_before_window", stats.Rejections.BeforeWindow,
		"rejected_after_window", stats.Rejections.AfterWindow,
		"accepted_inside_window", stats.InsideWindow,
		"reject_duplicate", stats.Rejections.Duplicate,
		"reject_other", stats.Rejections.Other,
		"reconciled", stats.Reconciled,
		"inactivated", stats.Reconciled,
		"final_status", finalStatus,
	}
	if finalStatus == string(providers.SyncRunCancelled) {
		logger.Info("event sync provider finished", attributes...)
		return finalErr
	}
	if finalErr != nil {
		if providers.SyncErrorStage(finalErr) == "" {
			finalErr = providers.WithSyncStage(finalErr, "import")
		}
		logger.Error("event sync provider finished", append(attributes, "error", "provider import failed", "stage", providers.SyncErrorStage(finalErr), "error_code", providers.SyncErrorCode(finalErr))...)
		return finalErr
	}
	logger.Info("event sync provider finished", attributes...)
	return nil
}

// Empty selection preserves the existing worker with both built-in providers.
func includesProvider(selection, name string) bool { return selection == "" || selection == name }
