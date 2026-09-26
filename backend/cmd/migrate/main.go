// Command migrate applies the embedded PostgreSQL schema migrations.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("migration command failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level := new(slog.LevelVar)
	level.Set(cfg.LogLevel)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	db, err := openDB(cfg.DatabaseURL)
	if err != nil {
		logger.Error("open database failed")
		return fmt.Errorf("open database failed")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := migrations.Up(ctx, db); err != nil {
		logger.Error("apply migrations failed", "error", err)
		return fmt.Errorf("apply migrations failed: %w", err)
	}
	logger.Info("migrations applied")
	return nil
}

func openDB(databaseURL string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	return stdlib.OpenDB(*config), nil
}
