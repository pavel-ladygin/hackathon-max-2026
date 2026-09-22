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
	providerName := flags.String("provider", "", "event provider (kudago)")
	cityName := flags.String("city", "", "catalog city (moscow)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *providerName != "kudago" {
		return errors.New("--provider must be kudago")
	}
	if *cityName != "moscow" {
		return errors.New("--city must be moscow")
	}

	cfg, err := config.Load()
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

	client, err := kudago.NewClient(kudago.Options{
		BaseURL: cfg.KudaGoBaseURL, Timeout: cfg.KudaGoTimeout,
		Location: cfg.KudaGoLocation, PageSize: cfg.KudaGoPageSize,
	})
	if err != nil {
		return err
	}
	repository := providers.NewRepository(db)
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	stats, err := client.Import(ctx, cityID, repository, func(err error) {
		logger.Error("event import failed", "error", err)
	})
	fmt.Printf("fetched=%d normalized=%d inserted=%d updated=%d skipped=%d errors=%d\n",
		stats.Fetched, stats.Normalized, stats.Inserted, stats.Updated, stats.Skipped, stats.Errors)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("import cancelled: %w", err)
		}
		return err
	}
	return nil
}
