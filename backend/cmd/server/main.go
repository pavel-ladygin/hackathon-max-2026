package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/adminauth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/analytics"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/auth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/config"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/discovery"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventresources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsources"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventsubmissions"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/eventuploads"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/maxwelcome"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/objectstorage"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/saved"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
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
	handler, err := newHandler(ctx, cfg, db, logger)
	if err != nil {
		return err
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		behavior.FlushOperationTelemetry(flushCtx)
	}()
	go behavior.RunRetentionPrune(ctx, db, logger)
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

func newHandler(ctx context.Context, cfg config.Config, db *store.Pool, logger *slog.Logger) (http.Handler, error) {
	var objects objectstorage.Store
	if cfg.RegS3Bucket != "" {
		configured, err := objectstorage.New(cfg.RegS3Endpoint, cfg.RegS3Region, cfg.RegS3Bucket, cfg.RegS3AccessKeyID, cfg.RegS3SecretAccessKey)
		if err != nil {
			return nil, err
		}
		objects = configured
	}
	uploadService := eventuploads.NewService(db, objects)
	if uploadService.Available() {
		go runEventUploadCleanup(ctx, uploadService, logger)
	}
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
	homeHandler := discovery.NewHomeHandler(discovery.NewHomeService(discoveryService, discoveryRepository, preferencesService, discoveryRepository))
	searchHandler := discovery.NewSearchHandler(discoveryService, discoveryRepository)
	detailHandler := discovery.NewDetailHandler(discoveryService)
	poolKeyInput := append([]byte("rooms-pool-tie-break\x00"), cfg.InviteEncryptionKey...)
	poolKey := sha256.Sum256(poolKeyInput)
	behaviorRecorder := behavior.Recorder{}
	homeHandler.EnableOperationTelemetry(behavior.DatabaseOperationSink(db, behaviorRecorder))
	behaviorService, err := behavior.NewService(db)
	if err != nil {
		return nil, err
	}
	poolBuilder, err := recommendations.NewPoolBuilderWithBehavior(catalog.NewRepository(db), poolKey[:], preferencesService, behaviorService)
	if err != nil {
		return nil, err
	}
	behaviorHandler := behavior.NewHandler(behaviorService)
	savedService, err := saved.NewService(db, behaviorRecorder, cfg.InviteEncryptionKey)
	if err != nil {
		return nil, err
	}
	savedHandler := saved.NewHandler(savedService)
	ticketService, err := tickets.NewService(db, behaviorRecorder, cfg.TicketProviderAllowlist)
	if err != nil {
		return nil, err
	}
	ticketHandler := tickets.NewHandler(ticketService)
	analyticsHandler := analytics.NewHandler(db)
	providerSecretCodec, err := sourceconfig.NewSecretCodec(cfg.InviteEncryptionKey, cfg.InviteEncryptionKeyVersion)
	if err != nil {
		return nil, err
	}
	sourceRepository, err := sourceconfig.NewRepository(db, providerSecretCodec)
	if err != nil {
		return nil, err
	}
	if err := sourceRepository.CheckSecrets(ctx); err != nil {
		return nil, err
	}
	eventSourcesHandler, err := eventsources.NewHandler(ctx, db, sourceRepository, eventsources.HandlerOptions{BuiltInEnabled: map[string]bool{"kudago": true, "timepad": cfg.TimepadToken != ""}})
	if err != nil {
		return nil, err
	}
	maxWelcomeHandler, err := maxwelcome.New(cfg.MAXBotToken, cfg.MAXAppURL, cfg.MAXBotWebhookSecret)
	if err != nil {
		return nil, err
	}

	roomService, err := rooms.NewCreateService(db, behaviorRecorder, invites, poolBuilder)
	if err != nil {
		return nil, err
	}
	roomEventsCursor, err := rooms.NewRoomEventsCursorCodec(cfg.InviteEncryptionKey)
	if err != nil {
		return nil, err
	}
	if err := roomService.EnableRoomEvents(catalog.NewRepository(db), roomEventsCursor); err != nil {
		return nil, err
	}
	authService.SetInviteContextResolver(roomService)
	go roomService.RunExpiryCleanup(ctx, time.Minute, func(err error) {
		logger.Error("room expiry cleanup failed", "error", err)
	})
	return adminauth.NewMiddleware(cfg.InternalAdminProxySecret)(httpapi.NewRouter(
		readiness{db: db},
		logger,
		authService.RegisterRoutes,
		func(r chi.Router) { preferencesHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { homeHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { searchHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { detailHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { behaviorHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { savedHandler.RegisterRoutes(r, authService.Middleware) },
		func(r chi.Router) { ticketHandler.RegisterRoutes(r, authService.Middleware) },
		analyticsHandler.RegisterRoutes,
		eventSourcesHandler.RegisterRoutes,
		func(r chi.Router) {
			eventsubmissions.NewHandlerWithUploads(db, generic.FetchImage, uploadService).RegisterRoutes(r, authService.Middleware)
			eventuploads.NewHandler(db, objects).RegisterRoutes(r, authService.Middleware)
		},
		eventresources.NewHandlerWithUploads(db, generic.FetchImage, uploadService).RegisterRoutes,
		func(r chi.Router) {
			r.Group(func(protected chi.Router) {
				protected.Use(authService.Middleware)
				roomService.RegisterRoutes(protected)
			})
		},
		func(r chi.Router) {
			if maxWelcomeHandler != nil {
				maxWelcomeHandler.RegisterRoutes(r)
			}
		},
	)), nil
}

func runEventUploadCleanup(ctx context.Context, uploads *eventuploads.Service, logger *slog.Logger) {
	cleanup := func() {
		if _, err := uploads.CleanupUnattached(ctx, 100); err != nil && ctx.Err() == nil {
			logger.Error("event image upload cleanup failed", "error", err)
		}
	}
	cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
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
