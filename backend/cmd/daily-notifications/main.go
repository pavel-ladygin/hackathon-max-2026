// Command daily-notifications sends opted-in users a daily MAX mini-app prompt.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/dailynotifications"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("daily notification process stopped", "error", err)
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
		return fmt.Errorf("open daily notification database: %w", err)
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return errors.New("daily notifications require current migrations; run cmd/migrate first")
	}
	token := strings.TrimSpace(os.Getenv("MAX_BOT_TOKEN"))
	appURL := strings.TrimSpace(os.Getenv("MAX_APP_URL"))
	sender, err := dailynotifications.New(db.Pool, token, appURL)
	if err != nil {
		return err
	}
	logger.Info("daily notifications starting", "schedule", "12:00 Europe/Moscow")
	for {
		now := time.Now()
		local := now.In(moscow())
		if local.Hour() >= 12 {
			if err := sender.RunOnce(ctx, now); err != nil {
				logger.Error("daily notification pass failed", "error", err)
			} else {
				logger.Info("daily notification pass completed")
			}
		}
		wait := time.Until(nextNoon(now))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			logger.Info("daily notifications stopped")
			return nil
		case <-timer.C:
		}
	}
}

var moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}
var nextNoon = func(now time.Time) time.Time {
	loc := moscow()
	local := now.In(loc)
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	if !local.Before(noon) {
		noon = noon.AddDate(0, 0, 1)
	}
	return noon
}
