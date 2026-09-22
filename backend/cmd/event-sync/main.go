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
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/kudago"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

const (
	kudaGoPriority  = 100
	timepadPriority = 50
)

type eventImporter interface {
	Import(context.Context, uuid.UUID, providers.EventStore, func(error)) (providers.ImportStats, error)
}

type syncProvider struct {
	name     string
	priority int
	importer eventImporter
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

	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	var cityExists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM cities WHERE id = $1)", cityID).Scan(&cityExists); err != nil {
		return fmt.Errorf("check Moscow catalog city: %w", err)
	}
	if !cityExists {
		return errors.New("Moscow catalog city is missing; initialize the catalog first")
	}

	kudaGoClient, err := kudago.NewClient(kudago.Options{
		BaseURL:  cfg.KudaGoBaseURL,
		Timeout:  cfg.KudaGoTimeout,
		Location: cfg.KudaGoLocation,
		PageSize: cfg.KudaGoPageSize,
	})
	if err != nil {
		return err
	}
	syncProviders := []syncProvider{{name: "kudago", priority: kudaGoPriority, importer: kudaGoClient}}
	if cfg.TimepadToken == "" {
		logger.Warn("event sync provider disabled", "provider", "timepad", "reason", "TIMEPAD_TOKEN is not configured")
	} else {
		timepadClient, err := timepad.NewClient(timepad.Options{
			BaseURL:  cfg.TimepadBaseURL,
			Token:    cfg.TimepadToken,
			Timeout:  cfg.TimepadTimeout,
			PageSize: cfg.TimepadPageSize,
		})
		if err != nil {
			return err
		}
		syncProviders = append(syncProviders, syncProvider{name: "timepad", priority: timepadPriority, importer: timepadClient})
	}

	repository := providers.NewRepository(db)
	logger.Info("event sync starting", "providers", len(syncProviders), "city", "moscow", "interval", cfg.EventSyncInterval)
	for {
		for _, provider := range syncProviders {
			if err := syncOne(ctx, cityID, repository, provider, logger); err != nil && ctx.Err() != nil {
				logger.Info("event sync shutdown complete")
				return nil
			}
		}

		timer := time.NewTimer(cfg.EventSyncInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			logger.Info("event sync shutdown complete")
			return nil
		case <-timer.C:
		}
	}
}

func syncOne(ctx context.Context, cityID uuid.UUID, repository providers.EventStore, provider syncProvider, logger *slog.Logger) error {
	startedAt := time.Now()
	stats, syncErr := provider.importer.Import(ctx, cityID, repository, func(err error) {
		logger.Error("event sync record failed", "provider", provider.name, "priority", provider.priority, "error", err)
	})
	attributes := []any{
		"provider", provider.name,
		"priority", provider.priority,
		"duration", time.Since(startedAt),
		"fetched", stats.Fetched,
		"normalized", stats.Normalized,
		"inserted", stats.Inserted,
		"updated", stats.Updated,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
	}
	if syncErr != nil {
		logger.Error("event sync provider failed", append(attributes, "error", syncErr)...)
		return syncErr
	}
	logger.Info("event sync provider completed", attributes...)
	return nil
}
