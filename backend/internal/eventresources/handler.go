// Package eventresources serves stored Generic resources under runtime policy.
package eventresources

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

type ImageFetcher func(context.Context, string) ([]byte, string, error)

type Handler struct {
	db      *store.Pool
	domains *resourcedomains.Repository
	fetch   ImageFetcher
	slots   chan struct{}
}

func NewHandler(db *store.Pool) *Handler { return NewHandlerWithFetcher(db, generic.FetchImage) }

// NewHandlerWithFetcher permits deterministic upstream fixtures; production
// always uses NewHandler and the Generic connector's secure transport.
func NewHandlerWithFetcher(db *store.Pool, fetch ImageFetcher) *Handler {
	domains, err := resourcedomains.NewRepository(db)
	if err != nil || fetch == nil {
		panic("resource handler dependencies are required")
	}
	return &Handler{db: db, domains: domains, fetch: fetch, slots: make(chan struct{}, 16)}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/api/v1/event-images/{imageId}/content", h.image)
	r.Get("/api/v1/events/{eventId}/ticket", h.ticket)
}

func resourceError(w http.ResponseWriter, r *http.Request, status int) {
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: "RESOURCE_UNAVAILABLE", Message: "Resource unavailable"})
}

func resourceID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	// Neither endpoint accepts a client-selected destination, even as an ignored
	// query parameter. Never cache approvals or redirects across requests.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.RawQuery != "" {
		resourceError(w, r, http.StatusBadRequest)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		resourceError(w, r, http.StatusNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) allowed(ctx context.Context, sourceID uuid.UUID, raw string, purpose resourcedomains.Purpose) (bool, error) {
	host, err := resourcedomains.ResourceHostname(raw)
	if err != nil {
		return false, nil
	}
	return h.domains.Allowed(ctx, sourceID, host, purpose)
}

func (h *Handler) image(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r, "imageId")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), generic.HTTPTimeout)
	defer cancel()
	var raw string
	var sourceID uuid.UUID
	err := h.db.QueryRow(ctx, `SELECT i.url,s.id FROM event_images i JOIN events e ON e.id=i.event_id JOIN event_sources s ON s.source_key=e.source WHERE i.id=$1 AND s.source_key LIKE 'generic:%' AND e.provider_active AND NOT e.is_demo AND e.starts_at>now()`, id).Scan(&raw, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		resourceError(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		resourceError(w, r, http.StatusServiceUnavailable)
		return
	}
	allowed, err := h.allowed(ctx, sourceID, raw, resourcedomains.PurposeImage)
	if err != nil {
		resourceError(w, r, http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		resourceError(w, r, http.StatusForbidden)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		resourceError(w, r, http.StatusServiceUnavailable)
		return
	}
	body, contentType, err := h.fetch(ctx, raw)
	if err != nil {
		resourceError(w, r, http.StatusBadGateway)
		return
	}
	// A revoke completed while the upstream was in flight also blocks this
	// response. No DB connection is held during upstream IO.
	allowed, err = h.allowed(ctx, sourceID, raw, resourcedomains.PurposeImage)
	if err != nil || !allowed {
		resourceError(w, r, http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) ticket(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r, "eventId")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var raw string
	var sourceID uuid.UUID
	err := h.db.QueryRow(ctx, `SELECT e.ticket_url,s.id FROM events e JOIN event_sources s ON s.source_key=e.source WHERE e.id=$1 AND s.source_key LIKE 'generic:%' AND e.provider_active AND NOT e.is_demo AND e.status='published' AND e.starts_at>now() AND e.ticket_available AND e.ticket_url IS NOT NULL`, id).Scan(&raw, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		resourceError(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		resourceError(w, r, http.StatusServiceUnavailable)
		return
	}
	allowed, err := h.allowed(ctx, sourceID, raw, resourcedomains.PurposeTicket)
	if err != nil {
		resourceError(w, r, http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		resourceError(w, r, http.StatusForbidden)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, raw, http.StatusFound)
}
