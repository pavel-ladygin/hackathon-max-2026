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
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

const (
	kudaGoPriority  = 100
	timepadPriority = 50
)

var errPartialProviderSync = providers.SafeError("provider_persistence_error")
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
	syncProviders, err := builtInProviders(cfg, cityTimezone, lockProvider)
	if err != nil {
		return err
	}
	for _, name := range []string{"kudago", "timepad"} {
		enabled, cap := cfg.EventSyncKudaGoEnabled, cfg.KudaGoMaxPagesPerRun
		if name == "timepad" {
			enabled, cap = cfg.EventSyncTimepadEnabled, cfg.TimepadMaxPagesPerRun
		}
		logger.Info("provider configuration", "provider", name, "provider_enabled", enabled, "city_id", cityID, "max_pages", cap, "upsert_only", cfg.EventSyncUpsertOnly || cap > 0, "max_duration", cfg.EventSyncMaxRunDuration)
	}
	blocked := make(map[string]bool)

	repository := providers.NewRepository(db)
	var genericRunner *eventsources.Runner
	if sourceRepository != nil {
		genericRunner, err = eventsources.NewRunner(db, sourceRepository, uuid.MustParse(catalogseed.MoscowCityID))
		if err != nil {
			return err
		}
	}
	logger.Info("event sync starting", "providers", len(syncProviders), "city_id", cityID, "city", cityName, "timezone", cityTimezone, "interval", cfg.EventSyncInterval, "run_on_start", cfg.EventSyncRunOnStart)
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
		if err := syncProvidersCycle(ctx, cityID, repository, cycleProviders, logger, blocked); err != nil {
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
	return syncProvidersCycle(ctx, cityID, repository, syncProviders, logger, nil)
}

func syncProvidersCycle(ctx context.Context, cityID uuid.UUID, repository providers.SyncStore, syncProviders []syncProvider, logger *slog.Logger, blocked map[string]bool) error {
	for _, provider := range syncProviders {
		if blocked[provider.name] {
			continue
		}
		err := syncOne(ctx, cityID, repository, provider, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if (provider.name == "kudago" || provider.name == "timepad") && providers.IsBlockingError(err) && blocked != nil {
			blocked[provider.name] = true
			logger.Error("provider blocked until operator restart", "provider", provider.name, "error_code", providers.SyncErrorCode(err))
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
		logger.Error("event sync record failed", "provider", provider.name, "priority", provider.priority, "error", "provider record persistence failed")
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
		"scope", stats.Scope,
		"limit_reason", stats.LimitReason,
		"http_attempts", stats.HTTPAttempts,
		"initial_total", stats.InitialTotal,
		"final_offset", stats.FinalOffset,
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
		logger.Error("event sync provider finished", append(attributes, "error", "provider import failed", "error_code", providers.SyncErrorCode(finalErr))...)
		return finalErr
	}
	logger.Info("event sync provider finished", attributes...)
	return nil
}
