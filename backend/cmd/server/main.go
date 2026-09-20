package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/auth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	if cfg.MAXBotToken == "" {
		return fmt.Errorf("MAX_BOT_TOKEN is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable")
		return fmt.Errorf("database unavailable")
	}
	defer db.Close()
	handler, err := newHandler(cfg, db, logger)
	if err != nil {
		return err
	}
	server := httpapi.NewServer(cfg.HTTPAddr, handler)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	logger.Info("http server starting", "environment", cfg.AppEnv, "address", cfg.HTTPAddr)

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server failed")
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		_ = server.Close()
		return fmt.Errorf("graceful shutdown failed")
	}
	return nil
}

func newHandler(cfg config.Config, db *store.Pool, logger *slog.Logger) (http.Handler, error) {
	if len(cfg.InviteEncryptionKey) == 0 {
		return nil, fmt.Errorf("INVITE_ENCRYPTION_KEY is required")
	}
	if cfg.InviteURLTemplate == "" || cfg.MAXDeepLinkTemplate == "" {
		return nil, fmt.Errorf("INVITE_URL_TEMPLATE and MAX_DEEP_LINK_TEMPLATE are required")
	}
	invites, err := rooms.NewInviteCodec(cfg.InviteEncryptionKey, cfg.InviteEncryptionKeyVersion, cfg.InviteURLTemplate, cfg.MAXDeepLinkTemplate)
	if err != nil {
		return nil, err
	}
	authService, err := auth.NewServiceWithTrustedProxyCIDRs(db, cfg.MAXBotToken, cfg.MAXInitDataMaxAge, cfg.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	preferencesService := preferences.NewService(db)
	preferencesHandler := preferences.NewHandler(preferencesService)
	discoveryRepository := discovery.NewRepository(db)
	cursorCodec, err := discovery.NewCursorCodec(cfg.InviteEncryptionKey)
	if err != nil {
		return nil, err
	}
	discoveryService := discovery.NewService(discoveryRepository, cursorCodec)
	homeHandler := discovery.NewHomeHandler(discovery.NewHomeService(discoveryService, discoveryRepository, preferencesService))
	searchHandler := discovery.NewSearchHandler(discoveryService, discoveryRepository)
	detailHandler := discovery.NewDetailHandler(discoveryService)
	roomService, err := rooms.NewCreateService(db, behavior.Recorder{}, invites)
	if err != nil {
		return nil, err
	}
	return httpapi.NewRouter(
		readiness{db: db},
		logger,
		authService.RegisterRoutes,
		func(r chi.Router) { preferencesHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { homeHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { searchHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { detailHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) {
			r.Group(func(protected chi.Router) {
				protected.Use(authService.Middleware)
				roomService.RegisterRoutes(protected)
			})
		},
	), nil
}

type readiness struct{ db *store.Pool }

func (r readiness) Ready(ctx context.Context) error {
	if err := r.db.Ping(ctx); err != nil {
		return err
	}
	return migrations.CheckCurrent(ctx, r.db.Pool)
}

func newLogger(level slog.Level) *slog.Logger {
	parsed := new(slog.LevelVar)
	parsed.Set(level)
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed}))
}
