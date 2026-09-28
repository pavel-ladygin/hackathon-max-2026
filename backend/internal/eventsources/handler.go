package eventsources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const maxConfigBodyBytes = 256 << 10

type Handler struct {
	db             *store.Pool
	sources        *sourceconfig.Repository
	domains        *resourcedomains.Repository
	runner         *Runner
	appCtx         context.Context
	cityID         uuid.UUID
	builtInEnabled map[string]bool
	previewFn      func(context.Context, sourceconfig.Input, string) (PreviewResult, error)
}

type HandlerOptions struct {
	BuiltInEnabled map[string]bool
}

func NewHandler(ctx context.Context, db *store.Pool, sources *sourceconfig.Repository, options ...HandlerOptions) (*Handler, error) {
	if db == nil || sources == nil {
		return nil, errors.New("event source dependencies are required")
	}
	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	runner, err := NewRunner(db, sources, cityID)
	if err != nil {
		return nil, err
	}
	domains, err := resourcedomains.NewRepository(db)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{"kudago": true, "timepad": false}
	if len(options) > 0 {
		for key, value := range options[0].BuiltInEnabled {
			enabled[key] = value
		}
	}
	return &Handler{db: db, sources: sources, domains: domains, runner: runner, appCtx: ctx, cityID: cityID, builtInEnabled: enabled, previewFn: Preview}, nil
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/internal/event-sources", func(r chi.Router) {
		r.Use(noStore)
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Post("/test", h.test)
		r.Get("/{id}", h.get)
		r.Patch("/{id}", h.update)
		r.Post("/{id}/sync", h.sync)
		r.Get("/{id}/domains", h.listDomains)
		r.Post("/{id}/domains", h.addDomain)
		r.Delete("/{id}/domains/{domainId}", h.revokeDomain)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func requireMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Admin-Request") != "1" {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Admin request header is required")
		return false
	}
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" || origin.User != nil || origin.Fragment != "" || !strings.EqualFold(origin.Host, r.Host) || origin.Path != "" || origin.RawQuery != "" {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Same-origin request is required")
		return false
	}
	if origin.Scheme != "https" && !(origin.Scheme == "http" && (r.Host == "localhost" || strings.HasPrefix(r.Host, "localhost:") || strings.HasPrefix(r.Host, "127.0.0.1:"))) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Secure origin is required")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes+1))
	if err != nil || len(body) > maxConfigBodyBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "VALIDATION_FAILED", "JSON body exceeds size limit")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "JSON body must contain one object within the size limit")
		return false
	}
	return true
}

func sourceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid source ID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	sources, err := h.sources.List(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
		return
	}
	counts := map[string]int64{}
	rows, err := h.db.Query(r.Context(), `SELECT source, count(*) FROM events WHERE provider_active=true AND is_demo=false GROUP BY source`)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
		return
	}
	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			rows.Close()
			writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
			return
		}
		counts[source] = count
	}
	rows.Close()
	if rows.Err() != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
		return
	}
	lastRuns := map[string]lastRun{}
	rows, err = h.db.Query(r.Context(), `SELECT DISTINCT ON (provider) provider,state,completed_at FROM provider_sync_runs ORDER BY provider,started_at DESC`)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
		return
	}
	for rows.Next() {
		var source string
		var run lastRun
		if err := rows.Scan(&source, &run.State, &run.At); err != nil {
			rows.Close()
			writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
			return
		}
		lastRuns[source] = run
	}
	rows.Close()
	if rows.Err() != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event sources unavailable")
		return
	}
	result := make([]map[string]any, 0, len(sources)+2)
	for _, builtIn := range []struct{ key, name string }{{"kudago", "KudaGo"}, {"timepad", "Timepad"}} {
		result = append(result, map[string]any{"kind": "built_in", "source_key": builtIn.key, "name": builtIn.name, "enabled": h.builtInEnabled[builtIn.key], "event_count": counts[builtIn.key], "last_sync_state": lastRuns[builtIn.key].State, "last_sync_at": lastRuns[builtIn.key].At})
	}
	for _, source := range sources {
		encoded, _ := json.Marshal(source)
		var item map[string]any
		_ = json.Unmarshal(encoded, &item)
		item["kind"] = "generic"
		item["event_count"] = counts[source.SourceKey]
		item["last_sync_state"] = lastRuns[source.SourceKey].State
		item["last_sync_at"] = lastRuns[source.SourceKey].At
		result = append(result, item)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"sources": result})
}

type lastRun struct {
	State string     `json:"last_sync_state,omitempty"`
	At    *time.Time `json:"last_sync_at,omitempty"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	source, err := h.sources.Get(r.Context(), id)
	if errors.Is(err, sourceconfig.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event source unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"source": source})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	var input sourceconfig.Input
	if !decodeBody(w, r, &input) {
		return
	}
	if err := ValidateInput(input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	source, err := h.sources.Create(r.Context(), input)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Event source could not be created")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"source": source})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	var input sourceconfig.Input
	if !decodeBody(w, r, &input) {
		return
	}
	if err := ValidateInput(input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	source, err := h.sources.Update(r.Context(), id, input)
	if errors.Is(err, sourceconfig.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
		return
	}
	if errors.Is(err, sourceconfig.ErrIdentityImmutable) || errors.Is(err, sourceconfig.ErrMappingImmutable) {
		writeError(w, r, http.StatusConflict, "CONFLICT", "Event source identity is locked")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Event source could not be updated")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"source": source})
}

type testRequest struct {
	SourceID *uuid.UUID `json:"source_id,omitempty"`
	sourceconfig.Input
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	var request testRequest
	if !decodeBody(w, r, &request) {
		return
	}
	secret := request.Secret
	if secret == "" && request.SourceID != nil {
		saved, savedSecret, err := h.sources.GetForExecution(r.Context(), *request.SourceID)
		if err != nil {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
			return
		}
		if request.SourceKey != saved.SourceKey || request.EndpointURL != saved.EndpointURL || request.AuthType != saved.AuthType || request.AuthName != saved.AuthName || !maps.Equal(request.QueryParams, saved.QueryParams) {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Provide a secret to test a changed connection")
			return
		}
		secret = savedSecret
	}
	previewCtx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, err := h.previewFn(previewCtx, request.Input, secret)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Event source test failed")
		return
	}
	if request.SourceID != nil {
		saved, getErr := h.sources.Get(previewCtx, *request.SourceID)
		if getErr != nil || saved.SourceKey != request.SourceKey || !strings.HasPrefix(saved.SourceKey, "generic:") {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Preview source identity is invalid")
			return
		}
		applyApprovedDomains(&result, func(hostname string, purpose resourcedomains.Purpose) (bool, error) {
			return h.domains.Allowed(previewCtx, saved.ID, hostname, purpose)
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) genericSource(w http.ResponseWriter, r *http.Request) (sourceconfig.Source, bool) {
	id, ok := sourceID(w, r)
	if !ok {
		return sourceconfig.Source{}, false
	}
	source, err := h.sources.Get(r.Context(), id)
	if errors.Is(err, sourceconfig.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
		return sourceconfig.Source{}, false
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Event source unavailable")
		return sourceconfig.Source{}, false
	}
	if !strings.HasPrefix(source.SourceKey, "generic:") {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
		return sourceconfig.Source{}, false
	}
	return source, true
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	source, ok := h.genericSource(w, r)
	if !ok {
		return
	}
	domains, err := h.domains.List(r.Context(), source.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Resource domains unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"domains": domains})
}

type domainInput struct {
	Hostname string                  `json:"hostname"`
	Purpose  resourcedomains.Purpose `json:"purpose"`
}

func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	source, ok := h.genericSource(w, r)
	if !ok {
		return
	}
	var input domainInput
	if !decodeBody(w, r, &input) {
		return
	}
	hostname, err := resourcedomains.NormalizeHostname(input.Hostname)
	if err != nil || !resourcedomains.ValidPurpose(input.Purpose) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Hostname or resource purpose is invalid")
		return
	}
	domain, err := h.domains.Add(r.Context(), source.ID, hostname, input.Purpose)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Resource domain could not be approved")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"domain": domain})
}

func (h *Handler) revokeDomain(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	source, ok := h.genericSource(w, r)
	if !ok {
		return
	}
	domainID, err := uuid.Parse(chi.URLParam(r, "domainId"))
	if err != nil || domainID == uuid.Nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid resource domain ID")
		return
	}
	revoked, err := h.domains.Revoke(r.Context(), source.ID, domainID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Resource domain unavailable")
		return
	}
	if !revoked {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource domain not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	err := h.runner.Start(h.appCtx, id)
	if errors.Is(err, sourceconfig.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Event source not found")
		return
	}
	if errors.Is(err, ErrSyncRunning) {
		writeError(w, r, http.StatusConflict, "CONFLICT", "Sync already running")
		return
	}
	if errors.Is(err, ErrSourceDisabled) || errors.Is(err, ErrSourceInvalid) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Event source is unavailable")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "INTERNAL", "Sync unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
