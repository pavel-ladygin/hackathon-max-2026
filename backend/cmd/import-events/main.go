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
	"syscall"

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
	cityName := flags.String("city", "", "catalog city (moscow)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *providerName != "kudago" && *providerName != "timepad" {
		return errors.New("--provider must be kudago or timepad")
	}
	if *cityName != "moscow" {
		return errors.New("--city must be moscow")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client, err := newImporter(*providerName, cfg)
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

	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM cities WHERE id = $1)", cityID).Scan(&exists); err != nil {
		return fmt.Errorf("check Moscow catalog city: %w", err)
	}
	if !exists {
		return errors.New("Moscow catalog city is missing; initialize the catalog first")
	}

	repository := providers.NewRepository(db)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	stats, err := client.Import(ctx, cityID, repository, func(err error) {
		logger.Error("event import record failed", "error", "provider record persistence failed")
	})
	finalStatus := string(providers.SyncRunSucceeded)
	if errors.Is(err, context.Canceled) {
		finalStatus = string(providers.SyncRunCancelled)
	} else if err != nil || stats.Errors > 0 {
		finalStatus = string(providers.SyncRunFailed)
	}
	fmt.Printf("sync_run_id=%s pages_fetched=%d fetched=%d matched=%d normalized=%d inserted=%d updated=%d skipped=%d errors=%d reconciled=%d final_status=%s\n",
		stats.SyncRunID, stats.PagesFetched, stats.Fetched, stats.Matched, stats.Normalized, stats.Inserted, stats.Updated, stats.Skipped, stats.Errors, stats.Reconciled, finalStatus)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("import cancelled: %w", err)
		}
		return err
	}
	if stats.Errors > 0 {
		return fmt.Errorf("import completed with %d record errors", stats.Errors)
	}
	return nil
}

type eventImporter interface {
	Import(context.Context, uuid.UUID, providers.SyncStore, func(error)) (providers.ImportStats, error)
}

func newImporter(providerName string, cfg config.Config) (eventImporter, error) {
	switch providerName {
	case "kudago":
		return kudago.NewClient(kudago.Options{
			BaseURL: cfg.KudaGoBaseURL, Timeout: cfg.KudaGoTimeout,
			Location: cfg.KudaGoLocation, PageSize: cfg.KudaGoPageSize,
		})
	case "timepad":
		if cfg.TimepadToken == "" {
			return nil, errors.New("TIMEPAD_TOKEN is required for --provider timepad")
		}
		return timepad.NewClient(timepad.Options{
			BaseURL: cfg.TimepadBaseURL, Token: cfg.TimepadToken,
			Timeout: cfg.TimepadTimeout, PageSize: cfg.TimepadPageSize,
		})
	default:
		return nil, errors.New("unsupported event provider")
	}
}
