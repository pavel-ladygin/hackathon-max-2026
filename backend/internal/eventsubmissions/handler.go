// Package eventsubmissions implements user event proposals and their moderation.
package eventsubmissions

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/adminauth"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

const (
	UserSourceID  = "11111111-1111-5111-8111-111111111130"
	maxBodyBytes  = 64 << 10
	maxPriceMinor = 100000000
)

var categoryLabels = []struct{ Slug, Label string }{
	{"concerts", "Концерты"}, {"cinema", "Кино"}, {"theatre", "Театр"},
	{"standup", "Стендап"}, {"exhibitions", "Выставки"}, {"sports", "Спорт"},
	{"food", "Еда"}, {"parties", "Вечеринки"}, {"festivals", "Фестивали"},
	{"walks", "Прогулки"}, {"other", "Другое"},
}

type Handler struct {
	db      *store.Pool
	domains *resourcedomains.Repository
	fetch   func(context.Context, string) ([]byte, string, error)
	slots   chan struct{}
}

func NewHandler(db *store.Pool) *Handler {
	return NewHandlerWithFetcher(db, generic.FetchImage)
}

// NewHandlerWithFetcher permits deterministic tests of poster policy and
// outages. Production code should use NewHandler.
func NewHandlerWithFetcher(db *store.Pool, fetch func(context.Context, string) ([]byte, string, error)) *Handler {
	domains, err := resourcedomains.NewRepository(db)
	if db == nil || err != nil || fetch == nil {
		panic("event submission dependencies are required")
	}
	return &Handler{db: db, domains: domains, fetch: fetch, slots: make(chan struct{}, 8)}
}

// RegisterRoutes mounts authenticated user APIs and internal APIs. The caller
// applies the supplied session middleware only to user routes and guards the
// /api/v1/internal prefix with its verified administrator middleware.
func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.Route("/api/v1", func(r chi.Router) {
		if authenticate != nil {
			r.With(authenticate).Get("/event-submissions/options", h.options)
			r.With(authenticate).Post("/event-submissions", h.submit)
			r.With(authenticate).Get("/me/event-submissions", h.mine)
		}
		r.Route("/internal/event-submissions", func(r chi.Router) {
			r.Get("/", h.queue)
			r.Get("/{id}", h.detail)
			r.Get("/{id}/image", h.pendingImage)
			r.Post("/{id}/approve", h.approve)
			r.Post("/{id}/reject", h.reject)
			r.Get("/domains", h.listDomains)
			r.Post("/domains", h.addDomain)
			r.Delete("/domains/{domainId}", h.deleteDomain)
		})
	})
}

type SubmissionInput struct {
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	CityID         uuid.UUID `json:"city_id"`
	VenueName      string    `json:"venue_name"`
	VenueAddress   string    `json:"venue_address"`
	StartsAtLocal  string    `json:"starts_at_local"`
	EndsAtLocal    *string   `json:"ends_at_local,omitempty"`
	PriceMode      string    `json:"price_mode"`
	PriceFromMinor *int32    `json:"price_from_minor,omitempty"`
	PriceToMinor   *int32    `json:"price_to_minor,omitempty"`
	TicketURL      *string   `json:"ticket_url,omitempty"`
	CategorySlug   string    `json:"category_slug"`
	ImageURL       *string   `json:"image_url,omitempty"`
	AgeRating      *string   `json:"age_rating,omitempty"`
}

type Submission struct {
	ID              uuid.UUID       `json:"id"`
	AuthorID        uuid.UUID       `json:"author_id"`
	AuthorName      *string         `json:"author_name,omitempty"`
	CityID          uuid.UUID       `json:"city_id"`
	StartsAt        time.Time       `json:"starts_at"`
	Timezone        string          `json:"timezone"`
	Payload         SubmissionInput `json:"payload"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
	DecidedAt       *time.Time      `json:"decided_at"`
	DecidedBy       *string         `json:"decided_by"`
	RejectionReason *string         `json:"rejection_reason"`
	EventID         *uuid.UUID      `json:"event_id"`
}

func (h *Handler) options(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT id,name,timezone FROM cities ORDER BY name`)
	if err != nil {
		serverError(w, r)
		return
	}
	type city struct {
		ID       uuid.UUID `json:"id"`
		Name     string    `json:"name"`
		Timezone string    `json:"timezone"`
	}
	cities := []city{}
	for rows.Next() {
		var c city
		if err := rows.Scan(&c.ID, &c.Name, &c.Timezone); err != nil {
			rows.Close()
			serverError(w, r)
			return
		}
		cities = append(cities, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		serverError(w, r)
		return
	}
	rows.Close()
	type category struct {
		Slug  string `json:"slug"`
		Label string `json:"label"`
	}
	categories := make([]category, 0, len(categoryLabels))
	for _, c := range categoryLabels {
		categories = append(categories, category{c.Slug, c.Label})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cities": cities, "categories": categories})
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		unauthorized(w, r)
		return
	}
	if r.URL.RawQuery != "" {
		badRequest(w, r, "INVALID_REQUEST", "Query parameters are not allowed")
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 200 {
		badRequest(w, r, "INVALID_IDEMPOTENCY_KEY", "A single valid Idempotency-Key is required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "Request body is too large or invalid")
		return
	}
	var input SubmissionInput
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		badRequest(w, r, "INVALID_BODY", "Request body is invalid")
		return
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		badRequest(w, r, "INVALID_BODY", "Request body must contain one JSON object")
		return
	}
	if err := validateInput(&input); err != nil {
		writeSubmissionValidationError(w, r, err)
		return
	}
	key := strings.TrimSpace(keys[0])
	hash := sha256.Sum256(body)
	var out Submission
	err = h.db.InTx(r.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, principal.UserID.String()); err != nil {
			return err
		}
		var existingHash []byte
		err := tx.QueryRow(r.Context(), `SELECT id,author_id,city_id,starts_at,timezone,status,created_at,decided_at,decided_by,rejection_reason,event_id,request_hash FROM event_submissions WHERE author_id=$1 AND idempotency_key=$2`, principal.UserID, key).Scan(&out.ID, &out.AuthorID, &out.CityID, &out.StartsAt, &out.Timezone, &out.Status, &out.CreatedAt, &out.DecidedAt, &out.DecidedBy, &out.RejectionReason, &out.EventID, &existingHash)
		if err == nil {
			if subtle.ConstantTimeCompare(existingHash, hash[:]) != 1 {
				return errIdempotencyConflict
			}
			var payload []byte
			if err := tx.QueryRow(r.Context(), `SELECT payload FROM event_submissions WHERE id=$1`, out.ID).Scan(&payload); err != nil {
				return err
			}
			return json.Unmarshal(payload, &out.Payload)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var count int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM event_submissions WHERE author_id=$1 AND created_at>now()-interval '1 hour'`, principal.UserID).Scan(&count); err != nil {
			return err
		}
		if count >= 5 {
			return errRateLimit
		}
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM event_submissions WHERE author_id=$1 AND created_at>now()-interval '24 hours'`, principal.UserID).Scan(&count); err != nil {
			return err
		}
		if count >= 20 {
			return errRateLimit
		}
		var timezone string
		if err = tx.QueryRow(r.Context(), `SELECT timezone FROM cities WHERE id=$1`, input.CityID).Scan(&timezone); err != nil {
			return err
		}
		loc, e := time.LoadLocation(timezone)
		if e != nil {
			return errors.New("city timezone is invalid")
		}
		start, e := time.ParseInLocation("2006-01-02T15:04", input.StartsAtLocal, loc)
		if e != nil {
			return errors.New("starts_at_local must use YYYY-MM-DDTHH:mm")
		}
		if !start.After(time.Now()) {
			return errors.New("event start must be in the future")
		}
		if input.EndsAtLocal != nil {
			end, e := time.ParseInLocation("2006-01-02T15:04", *input.EndsAtLocal, loc)
			if e != nil || !end.After(start) {
				return errors.New("ends_at_local must be after the start")
			}
		}
		payload, _ := json.Marshal(input)
		out = Submission{ID: uuid.New(), AuthorID: principal.UserID, CityID: input.CityID, StartsAt: start, Timezone: timezone, Payload: input, Status: "pending"}
		err = tx.QueryRow(r.Context(), `INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,status,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8) RETURNING created_at`, out.ID, out.AuthorID, out.CityID, out.StartsAt, out.Timezone, payload, key, hash[:]).Scan(&out.CreatedAt)
		return err
	})
	if errors.Is(err, errIdempotencyConflict) {
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key was already used with another request")
		return
	}
	if errors.Is(err, errRateLimit) {
		writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Submission limit reached")
		return
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeSubmissionValidationError(w, r, errors.New("city_id is not supported"))
		} else if validationField(err) != "" {
			writeSubmissionValidationError(w, r, err)
		} else {
			serverError(w, r)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": out.ID, "status": out.Status, "created_at": out.CreatedAt})
}

var (
	errIdempotencyConflict = errors.New("idempotency conflict")
	errRateLimit           = errors.New("submission rate limit")
)

func validateInput(in *SubmissionInput) error {
	trim := func(s *string) { *s = strings.TrimSpace(*s) }
	trim(&in.Title)
	trim(&in.Description)
	trim(&in.VenueName)
	trim(&in.VenueAddress)
	trim(&in.CategorySlug)
	if in.Title == "" || len([]rune(in.Title)) > 200 {
		return errors.New("title is required and must be at most 200 characters")
	}
	if in.Description == "" || len([]rune(in.Description)) > 10000 {
		return errors.New("description is required and must be at most 10000 characters")
	}
	if in.CityID == uuid.Nil {
		return errors.New("city_id is required")
	}
	if in.VenueName == "" || len([]rune(in.VenueName)) > 200 {
		return errors.New("venue_name is required and must be at most 200 characters")
	}
	if in.VenueAddress == "" || len([]rune(in.VenueAddress)) > 500 {
		return errors.New("venue_address is required and must be at most 500 characters")
	}
	if !catalogseed.IsCategorySlug(in.CategorySlug) {
		return errors.New("category_slug is invalid")
	}
	switch in.PriceMode {
	case "free":
		for _, p := range []*int32{in.PriceFromMinor, in.PriceToMinor} {
			if p != nil && *p != 0 {
				return errors.New("free events cannot include a nonzero price")
			}
		}
		zero := int32(0)
		in.PriceFromMinor = &zero
		in.PriceToMinor = &zero
	case "unknown":
		if in.PriceFromMinor != nil || in.PriceToMinor != nil {
			return errors.New("unknown price must not include numeric prices")
		}
	case "paid":
		if in.PriceFromMinor == nil || *in.PriceFromMinor <= 0 || *in.PriceFromMinor > maxPriceMinor {
			return errors.New("paid events require a price_from_minor within the supported range")
		}
		if in.PriceToMinor != nil && (*in.PriceToMinor < *in.PriceFromMinor || *in.PriceToMinor > maxPriceMinor) {
			return errors.New("price_to_minor must be at least price_from_minor and within the supported range")
		}
	default:
		return errors.New("price_mode must be free, paid, or unknown")
	}
	if in.TicketURL != nil {
		if strings.TrimSpace(*in.TicketURL) != "" {
			if len([]rune(*in.TicketURL)) > 2048 {
				return errors.New("ticket_url is too long")
			}
			if err := httpsURL(*in.TicketURL); err != nil {
				return errors.New("ticket_url must be a valid HTTPS URL")
			}
			*in.TicketURL = strings.TrimSpace(*in.TicketURL)
		} else {
			in.TicketURL = nil
		}
	}
	if in.PriceMode != "free" && in.TicketURL == nil {
		return errors.New("ticket_url is required unless the event is free")
	}
	if in.ImageURL != nil {
		if strings.TrimSpace(*in.ImageURL) != "" {
			if len([]rune(*in.ImageURL)) > 2048 {
				return errors.New("image_url is too long")
			}
			if err := httpsURL(*in.ImageURL); err != nil {
				return errors.New("image_url must be a valid HTTPS URL")
			}
			*in.ImageURL = strings.TrimSpace(*in.ImageURL)
		} else {
			in.ImageURL = nil
		}
	}
	if in.AgeRating != nil {
		switch *in.AgeRating {
		case "0+", "6+", "12+", "16+", "18+", "unknown":
		default:
			return errors.New("age_rating is invalid")
		}
	}
	return nil
}

func validationField(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	for _, field := range []string{"title", "description", "city_id", "venue_name", "venue_address", "starts_at_local", "ends_at_local", "price_mode", "price_from_minor", "price_to_minor", "ticket_url", "image_url", "category_slug", "age_rating"} {
		if strings.Contains(message, field) {
			return field
		}
	}
	if strings.Contains(message, "price") || strings.Contains(message, "free events") || strings.Contains(message, "unknown price") {
		return "price_mode"
	}
	if strings.Contains(message, "timezone") || strings.Contains(message, "event start") || strings.Contains(message, "future") {
		return "starts_at_local"
	}
	return ""
}

func writeSubmissionValidationError(w http.ResponseWriter, r *http.Request, err error) {
	field := validationField(err)
	if field == "" {
		field = "request"
	}
	reasons := map[string]string{
		"title":            "Укажите название длиной до 200 символов.",
		"description":      "Укажите описание длиной до 10 000 символов.",
		"city_id":          "Выберите поддерживаемый город.",
		"venue_name":       "Укажите название места проведения.",
		"venue_address":    "Укажите адрес места проведения.",
		"starts_at_local":  "Укажите корректное будущее время начала для выбранного города.",
		"ends_at_local":    "Время окончания должно быть позже времени начала.",
		"price_mode":       "Проверьте режим и значение цены.",
		"price_from_minor": "Укажите корректную цену в копейках.",
		"price_to_minor":   "Цена «до» должна быть не меньше цены «от».",
		"ticket_url":       "Для платного события укажите корректную HTTPS-ссылку.",
		"image_url":        "Укажите корректную HTTPS-ссылку на афишу длиной до 2048 символов.",
		"category_slug":    "Выберите категорию из списка.",
		"age_rating":       "Выберите допустимый возрастной рейтинг.",
		"request":          "Проверьте введённые данные.",
	}
	reason := reasons[field]
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "VALIDATION_FAILED", "message": "Проверьте данные мероприятия.", "request_id": httpapi.RequestID(r.Context()), "details": map[string]string{"field": field, "reason": reason}}})
}
func httpsURL(raw string) error { _, e := resourcedomains.ResourceHostname(raw); return e }

func normalizePlace(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func (h *Handler) mine(w http.ResponseWriter, r *http.Request) {
	p, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		unauthorized(w, r)
		return
	}
	limit, offset := page(r)
	items, err := h.list(r.Context(), p.UserID, "", limit, offset, false)
	if err != nil {
		serverError(w, r)
		return
	}
	var total int
	if err = h.db.QueryRow(r.Context(), `SELECT count(*) FROM event_submissions WHERE author_id=$1`, p.UserID).Scan(&total); err != nil {
		serverError(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (h *Handler) queue(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		badRequest(w, r, "INVALID_STATUS", "Status is invalid")
		return
	}
	limit, offset := page(r)
	items, err := h.list(r.Context(), uuid.Nil, status, limit, offset, true)
	if err != nil {
		serverError(w, r)
		return
	}
	var total int
	query := `SELECT count(*) FROM event_submissions WHERE ($1='' OR status=$1)`
	if err = h.db.QueryRow(r.Context(), query, status).Scan(&total); err != nil {
		serverError(w, r)
		return
	}
	counts := map[string]int{"pending": 0, "approved": 0, "rejected": 0}
	rows, err := h.db.Query(r.Context(), `SELECT status,count(*) FROM event_submissions GROUP BY status`)
	if err != nil {
		serverError(w, r)
		return
	}
	for rows.Next() {
		var s string
		var c int
		if err = rows.Scan(&s, &c); err != nil {
			rows.Close()
			serverError(w, r)
			return
		}
		counts[s] = c
	}
	rows.Close()
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "counts": counts})
}

func (h *Handler) list(ctx context.Context, author uuid.UUID, status string, limit, offset int, withAuthor bool) ([]Submission, error) {
	query := `SELECT s.id,s.author_id,s.city_id,s.starts_at,s.timezone,s.payload,s.status,s.created_at,s.decided_at,s.decided_by,s.rejection_reason,s.event_id,u.display_name FROM event_submissions s JOIN users u ON u.id=s.author_id WHERE ($1::uuid IS NULL OR s.author_id=$1) AND ($2='' OR s.status=$2) ORDER BY s.created_at DESC,s.id DESC LIMIT $3 OFFSET $4`
	var authorArg any
	if author != uuid.Nil {
		authorArg = author
	}
	rows, err := h.db.Query(ctx, query, authorArg, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Submission{}
	for rows.Next() {
		var s Submission
		var payload []byte
		if err := rows.Scan(&s.ID, &s.AuthorID, &s.CityID, &s.StartsAt, &s.Timezone, &payload, &s.Status, &s.CreatedAt, &s.DecidedAt, &s.DecidedBy, &s.RejectionReason, &s.EventID, &s.AuthorName); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &s.Payload); err != nil {
			return nil, err
		}
		if !withAuthor {
			s.AuthorName = nil
		}
		items = append(items, s)
	}
	return items, rows.Err()
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	items, err := h.listByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Submission not found")
		return
	}
	if err != nil {
		serverError(w, r)
		return
	}
	var duplicates []map[string]any
	rows, err := h.db.Query(r.Context(), `SELECT e.id,e.title,e.starts_at,v.name venue_name FROM events e JOIN venues v ON v.id=e.venue_id WHERE e.venue_id IN (SELECT v2.id FROM venues v2 WHERE v2.city_id=$1 AND lower(v2.name)=lower($2)) AND lower(e.title)=lower($3) AND e.starts_at BETWEEN $4-interval '1 day' AND $4+interval '1 day' AND e.status='published'`, items.CityID, items.Payload.VenueName, items.Payload.Title, items.StartsAt)
	if err != nil {
		serverError(w, r)
		return
	}
	duplicates = []map[string]any{}
	for rows.Next() {
		var event uuid.UUID
		var title, venue string
		var starts time.Time
		if err := rows.Scan(&event, &title, &starts, &venue); err != nil {
			rows.Close()
			serverError(w, r)
			return
		}
		duplicates = append(duplicates, map[string]any{"event_id": event, "title": title, "starts_at": starts, "venue_name": venue})
	}
	rows.Close()
	domains, err := h.domains.List(r.Context(), uuid.MustParse(UserSourceID))
	if err != nil {
		serverError(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submission": items, "duplicates": duplicates, "resource_domains": domains})
}
func (h *Handler) listByID(ctx context.Context, id uuid.UUID) (Submission, error) {
	var s Submission
	var payload []byte
	err := h.db.QueryRow(ctx, `SELECT s.id,s.author_id,s.city_id,s.starts_at,s.timezone,s.payload,s.status,s.created_at,s.decided_at,s.decided_by,s.rejection_reason,s.event_id,u.display_name FROM event_submissions s JOIN users u ON u.id=s.author_id WHERE s.id=$1`, id).Scan(&s.ID, &s.AuthorID, &s.CityID, &s.StartsAt, &s.Timezone, &payload, &s.Status, &s.CreatedAt, &s.DecidedAt, &s.DecidedBy, &s.RejectionReason, &s.EventID, &s.AuthorName)
	if err != nil {
		return Submission{}, err
	}
	if err = json.Unmarshal(payload, &s.Payload); err != nil {
		return Submission{}, err
	}
	return s, nil
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if err := decodeBody(w, r, &empty, 4096); err != nil {
		badRequest(w, r, "INVALID_BODY", "Approval request must be an empty JSON object")
		return
	}
	actor := adminauth.User(r)
	if actor == "" {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Administrator identity is required")
		return
	}
	var eventID uuid.UUID
	err := h.db.InTx(r.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		var s Submission
		var payload []byte
		err := tx.QueryRow(r.Context(), `SELECT id,author_id,city_id,starts_at,timezone,payload,status,event_id FROM event_submissions WHERE id=$1 FOR UPDATE`, id).Scan(&s.ID, &s.AuthorID, &s.CityID, &s.StartsAt, &s.Timezone, &payload, &s.Status, &s.EventID)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &s.Payload); err != nil {
			return err
		}
		if s.Status == "approved" && s.EventID != nil {
			eventID = *s.EventID
			return nil
		}
		if s.Status != "pending" {
			return errDecisionConflict
		}
		var currentTimezone string
		if err := tx.QueryRow(r.Context(), `SELECT timezone FROM cities WHERE id=$1`, s.CityID).Scan(&currentTimezone); err != nil {
			return err
		}
		if currentTimezone != s.Timezone {
			return errors.New("city timezone changed after submission; review the proposed time")
		}
		location, err := time.LoadLocation(currentTimezone)
		if err != nil {
			return errors.New("city timezone is invalid")
		}
		start, err := time.ParseInLocation("2006-01-02T15:04", s.Payload.StartsAtLocal, location)
		if err != nil || !start.Equal(s.StartsAt) {
			return errors.New("event start is invalid")
		}
		if !start.After(time.Now()) {
			return errors.New("event start must be in the future")
		}
		if err := validateInput(&s.Payload); err != nil {
			return err
		}
		if err := ensureDomains(r.Context(), tx, s.Payload.TicketURL, s.Payload.ImageURL); err != nil {
			return err
		}
		var venueID uuid.UUID
		venueNameKey, venueAddressKey := normalizePlace(s.Payload.VenueName), normalizePlace(s.Payload.VenueAddress)
		lockKey := venueNameKey + "|" + venueAddressKey + "|" + s.CityID.String()
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
			return err
		}
		err = tx.QueryRow(r.Context(), `SELECT id FROM venues WHERE city_id=$1 AND regexp_replace(lower(btrim(name)),'[[:space:]]+',' ','g')=$2 AND regexp_replace(lower(btrim(address)),'[[:space:]]+',' ','g')=$3 LIMIT 1`, s.CityID, venueNameKey, venueAddressKey).Scan(&venueID)
		if errors.Is(err, pgx.ErrNoRows) {
			venueID = uuid.New()
			_, err = tx.Exec(r.Context(), `INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type) VALUES($1,$2,$3,$4,NULL,NULL,'other')`, venueID, s.CityID, s.Payload.VenueName, s.Payload.VenueAddress)
		}
		if err != nil {
			return err
		}
		priceFrom, priceTo := s.Payload.PriceFromMinor, s.Payload.PriceToMinor
		if s.Payload.PriceMode == "free" {
			zero := int32(0)
			priceFrom = &zero
			priceTo = &zero
		}
		var ticket any
		available := false
		if s.Payload.TicketURL != nil {
			ticket = *s.Payload.TicketURL
			available = true
		}
		eventID = uuid.New()
		var end any
		if s.Payload.EndsAtLocal != nil {
			parsed, parseErr := time.ParseInLocation("2006-01-02T15:04", *s.Payload.EndsAtLocal, location)
			if parseErr != nil || !parsed.After(start) {
				return errors.New("ends_at_local must be after the start")
			}
			end = parsed
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO events(id,source,external_id,is_demo,title,description,venue_id,starts_at,ends_at,timezone,price_from_minor,price_to_minor,currency,ticket_url,ticket_available,status,age_rating,published_at,provider_active) VALUES($1,'user',$2,false,$3,$4,$5,$6,$7,$8,$9,$10,'RUB',$11,$12,'published',$13,now(),true)`, eventID, s.ID.String(), s.Payload.Title, s.Payload.Description, venueID, s.StartsAt, end, s.Timezone, priceFrom, priceTo, ticket, available, s.Payload.AgeRating)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO event_categories(event_id,category_slug,is_primary) VALUES($1,$2,true)`, eventID, s.Payload.CategorySlug); err != nil {
			return err
		}
		if s.Payload.ImageURL != nil {
			if _, err = tx.Exec(r.Context(), `INSERT INTO event_images(id,event_id,url,role,position) VALUES($1,$2,$3,'card',0)`, uuid.New(), eventID, *s.Payload.ImageURL); err != nil {
				return err
			}
		}
		_, err = tx.Exec(r.Context(), `UPDATE event_submissions SET status='approved',decided_at=now(),decided_by=$2,event_id=$3 WHERE id=$1`, id, actor, eventID)
		return err
	})
	if errors.Is(err, errDecisionConflict) {
		writeError(w, r, http.StatusConflict, "DECISION_CONFLICT", "Submission has already been rejected")
		return
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Submission not found")
		} else if strings.Contains(err.Error(), "domain") {
			writeError(w, r, http.StatusUnprocessableEntity, "DOMAIN_NOT_APPROVED", err.Error())
		} else if validationField(err) != "" {
			writeSubmissionValidationError(w, r, err)
		} else {
			serverError(w, r)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "approved", "event_id": eventID})
}

var errDecisionConflict = errors.New("submission already decided")

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	actor := adminauth.User(r)
	if actor == "" {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Administrator identity is required")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		badRequest(w, r, "INVALID_BODY", "Reason is required")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len([]rune(input.Reason)) > 2000 {
		badRequest(w, r, "VALIDATION_FAILED", "Reason must be between 1 and 2000 characters")
		return
	}
	var status string
	err := h.db.InTx(r.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		var oldReason *string
		err := tx.QueryRow(r.Context(), `SELECT status,rejection_reason FROM event_submissions WHERE id=$1 FOR UPDATE`, id).Scan(&status, &oldReason)
		if err != nil {
			return err
		}
		if status == "rejected" {
			return nil
		}
		if status != "pending" {
			return errDecisionConflict
		}
		_, err = tx.Exec(r.Context(), `UPDATE event_submissions SET status='rejected',decided_at=now(),decided_by=$2,rejection_reason=$3 WHERE id=$1`, id, actor, input.Reason)
		status = "rejected"
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Submission not found")
		return
	}
	if errors.Is(err, errDecisionConflict) {
		writeError(w, r, http.StatusConflict, "DECISION_CONFLICT", "Submission has already been approved")
		return
	}
	if err != nil {
		serverError(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "rejected"})
}

func ensureDomains(ctx context.Context, tx pgx.Tx, ticket, image *string) error {
	for _, item := range []struct {
		raw     *string
		purpose resourcedomains.Purpose
	}{{ticket, resourcedomains.PurposeTicket}, {image, resourcedomains.PurposeImage}} {
		if item.raw == nil {
			continue
		}
		host, err := resourcedomains.ResourceHostname(*item.raw)
		if err != nil {
			return errors.New("resource domain is invalid")
		}
		var approvalID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM event_source_allowed_domains WHERE source_id=$1 AND hostname=$2 AND purpose=$3 AND enabled FOR SHARE`, uuid.MustParse(UserSourceID), host, item.purpose).Scan(&approvalID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("resource domain has not been approved: " + host)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) pendingImage(w http.ResponseWriter, r *http.Request) {
	id, ok := submissionID(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		badRequest(w, r, "INVALID_REQUEST", "Query parameters are not allowed")
		return
	}
	var raw *string
	err := h.db.QueryRow(r.Context(), `SELECT payload->>'image_url' FROM event_submissions WHERE id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || raw == nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Pending image not found")
		return
	}
	if err != nil {
		serverError(w, r)
		return
	}
	host, err := resourcedomains.ResourceHostname(*raw)
	if err != nil {
		writeError(w, r, http.StatusForbidden, "RESOURCE_UNAVAILABLE", "Image domain is not approved")
		return
	}
	allowed, err := h.domains.Allowed(r.Context(), uuid.MustParse(UserSourceID), host, resourcedomains.PurposeImage)
	if err != nil {
		serverError(w, r)
		return
	}
	if !allowed {
		writeError(w, r, http.StatusForbidden, "RESOURCE_UNAVAILABLE", "Image domain is not approved")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeError(w, r, http.StatusServiceUnavailable, "RESOURCE_UNAVAILABLE", "Image service is busy")
		return
	}
	body, contentType, err := h.fetch(r.Context(), *raw)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "RESOURCE_UNAVAILABLE", "Image unavailable")
		return
	}
	allowed, err = h.domains.Allowed(r.Context(), uuid.MustParse(UserSourceID), host, resourcedomains.PurposeImage)
	if err != nil {
		serverError(w, r)
		return
	}
	if !allowed {
		writeError(w, r, http.StatusForbidden, "RESOURCE_UNAVAILABLE", "Image domain is not approved")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) {
	items, err := h.listDomainsData(r.Context())
	if err != nil {
		serverError(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": items})
}
func (h *Handler) listDomainsData(ctx context.Context) ([]resourcedomains.Domain, error) {
	return h.domains.List(ctx, uuid.MustParse(UserSourceID))
}
func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	var in struct {
		Hostname string `json:"hostname"`
		Purpose  string `json:"purpose"`
	}
	if err := decodeBody(w, r, &in, 4096); err != nil {
		badRequest(w, r, "INVALID_BODY", "Invalid domain")
		return
	}
	purpose := resourcedomains.Purpose(in.Purpose)
	if purpose != resourcedomains.PurposeImage && purpose != resourcedomains.PurposeTicket {
		badRequest(w, r, "INVALID_PURPOSE", "Purpose must be image or ticket")
		return
	}
	approval, err := h.domains.Add(r.Context(), uuid.MustParse(UserSourceID), in.Hostname, purpose)
	if err != nil {
		badRequest(w, r, "INVALID_DOMAIN", "Domain is invalid")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": approval.ID, "source_id": approval.SourceID, "hostname": approval.Hostname, "purpose": approval.Purpose, "created_at": approval.CreatedAt})
}
func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "domainId"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Domain approval not found")
		return
	}
	deleted, err := h.domains.Revoke(r.Context(), uuid.MustParse(UserSourceID), id)
	if err != nil {
		serverError(w, r)
		return
	}
	if !deleted {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Domain approval not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func submissionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if r.URL.RawQuery != "" {
		badRequest(w, r, "INVALID_REQUEST", "Query parameters are not allowed")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Submission not found")
		return uuid.Nil, false
	}
	return id, true
}
func page(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
func decodeBody(w http.ResponseWriter, r *http.Request, dst any, max int64) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
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
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func badRequest(w http.ResponseWriter, r *http.Request, code, message string) {
	writeError(w, r, http.StatusBadRequest, code, message)
}
func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid session is required")
}
func serverError(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusInternalServerError, "INTERNAL", "Request could not be completed")
}
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
