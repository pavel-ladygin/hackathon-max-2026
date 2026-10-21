// Command import-events performs a one-shot import from an external event provider.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("import-events", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	providerName := flags.String("provider", "", "event provider (kudago or timepad)")
	cityName := flags.String("city", "", "catalog city (moscow or configured city profile)")
	maxPages := flags.Int("max-pages", 0, "maximum pages (0 uses configured cap)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *providerName != "kudago" && *providerName != "timepad" {
		return errors.New("--provider must be kudago or timepad")
	}
	if *cityName != "moscow" && *cityName != "configured" {
		return errors.New("--city must be moscow or configured")
	}
	if *maxPages < 0 || *maxPages > 1000 {
		return errors.New("--max-pages must be between 0 and 1000")
	}
	enabled, err := config.ProviderEnabled("EVENT_SYNC_" + strings.ToUpper(*providerName) + "_ENABLED")
	if err != nil {
		return err
	}
	if !enabled {
		fmt.Printf("provider=%s status=disabled\n", *providerName)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client, err := newImporter(*providerName, cfg, *maxPages)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open importer database: %w", err)
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return errors.New("import requires current migrations; run cmd/migrate first")
	}

	cityID := cfg.EventSyncCityID
	if *cityName == "moscow" && cityID != uuid.MustParse(catalogseed.MoscowCityID) {
		return errors.New("--city=moscow requires the Moscow event sync city")
	}
	var databaseCityName, cityTimezone string
	if err := db.QueryRow(ctx, "SELECT name, timezone FROM cities WHERE id = $1", cityID).Scan(&databaseCityName, &cityTimezone); err != nil {
		return errors.New("configured catalog city is missing or unavailable; initialize it first")
	}
	if err := cfg.ValidateSyncCity(databaseCityName, cityTimezone); err != nil {
		return err
	}

	repository := providers.NewRepository(db)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	{
		lock, acquired, err := providers.TryAcquireProviderSyncLock(ctx, db, *providerName)
		if err != nil {
			return fmt.Errorf("acquire %s sync lock: %w", *providerName, err)
		}
		if !acquired {
			logger.Info("provider_sync_skipped_already_running", "provider", *providerName)
			return nil
		}
		defer func() {
			if err := lock.Release(); err != nil {
				logger.Error("provider sync lock release failed", "provider", *providerName)
			}
		}()
	}
	startedAt := time.Now()
	stats, err := client.Import(ctx, cityID, repository, func(err error) {
		logger.Error("event import record failed", "error", "provider record persistence failed")
	})
	finalStatus := string(providers.SyncRunSucceeded)
	if errors.Is(err, context.Canceled) {
		finalStatus = string(providers.SyncRunCancelled)
	} else if err != nil || stats.Errors > 0 {
		finalStatus = string(providers.SyncRunFailed)
	}
	fmt.Printf("city_id=%s scope=%s limit_reason=%s http_attempts=%d initial_total=%d final_offset=%d duration=%s sync_run_id=%s pages_fetched=%d fetched=%d matched=%d normalized=%d inserted=%d updated=%d skipped=%d errors=%d reconciled=%d rejected=%d reject_city=%d reject_invalid_id=%d reject_missing_title=%d reject_missing_starts_at=%d reject_invalid_starts_at=%d reject_malformed_categories=%d rejected_before_window=%d rejected_after_window=%d accepted_inside_window=%d reject_duplicate=%d reject_other=%d final_status=%s error_code=%s\n",
		cityID, stats.Scope, stats.LimitReason, stats.HTTPAttempts, stats.InitialTotal, stats.FinalOffset, time.Since(startedAt), stats.SyncRunID, stats.PagesFetched, stats.Fetched, stats.Matched, stats.Normalized, stats.Inserted, stats.Updated, stats.Skipped, stats.Errors, stats.Reconciled,
		stats.Rejections.Total(), stats.Rejections.City, stats.Rejections.InvalidID, stats.Rejections.MissingTitle, stats.Rejections.MissingStartsAt, stats.Rejections.InvalidStartsAt, stats.Rejections.MalformedCategories, stats.Rejections.BeforeWindow, stats.Rejections.AfterWindow, stats.InsideWindow, stats.Rejections.Duplicate, stats.Rejections.Other, finalStatus, providers.SyncErrorCode(err))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("import cancelled: %w", err)
		}
		return providers.SafeError(providers.SyncErrorCode(err))
	}
	if stats.Errors > 0 {
		return fmt.Errorf("import completed with %d record errors", stats.Errors)
	}
	return nil
}

type eventImporter interface {
	Import(context.Context, uuid.UUID, providers.SyncStore, func(error)) (providers.ImportStats, error)
}

func newImporter(providerName string, cfg config.Config, maxPages int) (eventImporter, error) {
	maxPages = cappedPages(maxPages, providerPageCap(providerName, cfg))
	switch providerName {
	case "kudago":
		return kudago.NewClient(kudago.Options{
			BaseURL: cfg.KudaGoBaseURL, Timeout: cfg.KudaGoTimeout,
			Location: cfg.KudaGoLocation, PageSize: cfg.KudaGoPageSize,
			CityID: cfg.EventSyncCityID, Timezone: syncTimezone(cfg), MaxRequestsPerMinute: cfg.KudaGoMaxRequestsPerMinute,
			MaxPagesPerRun: maxPages, MaxRunDuration: cfg.EventSyncMaxRunDuration, UpsertOnly: cfg.EventSyncUpsertOnly,
		})
	case "timepad":
		if cfg.TimepadToken == "" {
			return nil, errors.New("TIMEPAD_TOKEN is required for --provider timepad")
		}
		cityName := cfg.TimepadCityName
		if cityName == "" {
			if profile, ok := cfg.SyncCityProfiles[cfg.EventSyncCityID]; ok {
				cityName = profile.TimepadCityName
			}
		}
		if cityName == "" {
			cityName = "Москва"
		}
		client, err := timepad.NewClient(timepad.Options{
			BaseURL: cfg.TimepadBaseURL, Token: cfg.TimepadToken,
			Timeout: cfg.TimepadTimeout, PageSize: cfg.TimepadPageSize,
			MaxRequestsPerMinute: cfg.TimepadMaxRequestsPerMinute,
			CityID:               cfg.EventSyncCityID, CityName: cityName, Timezone: syncTimezone(cfg),
			MaxPagesPerRun: maxPages, MaxRunDuration: cfg.EventSyncMaxRunDuration, UpsertOnly: cfg.EventSyncUpsertOnly,
		})
		if err != nil {
			return nil, err
		}
		return limitedTimepadImporter{client: client, maxPages: maxPages}, nil
	default:
		return nil, errors.New("unsupported event provider")
	}
}

func providerPageCap(providerName string, cfg config.Config) int {
	if providerName == "timepad" {
		return cfg.TimepadMaxPagesPerRun
	}
	return cfg.KudaGoMaxPagesPerRun
}

func cappedPages(requested, configured int) int {
	if requested == 0 || (configured > 0 && requested > configured) {
		return configured
	}
	return requested
}

func syncTimezone(cfg config.Config) string {
	if profile, ok := cfg.SyncCityProfiles[cfg.EventSyncCityID]; ok {
		return profile.Timezone
	}
	return "Europe/Moscow"
}

type limitedTimepadImporter struct {
	client   *timepad.Client
	maxPages int
}

func (importer limitedTimepadImporter) Import(ctx context.Context, cityID uuid.UUID, store providers.SyncStore, reportError func(error)) (providers.ImportStats, error) {
	return importer.client.ImportPages(ctx, cityID, store, reportError, importer.maxPages)
}
