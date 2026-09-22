package discovery

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

// DetailProvider is the discovery capability used by the event-detail transport.
type DetailProvider interface {
	Get(context.Context, uuid.UUID, uuid.UUID, *Location) (Detail, error)
}

// DetailHandler exposes the authenticated event-detail route.
type DetailHandler struct{ service DetailProvider }

func NewDetailHandler(service DetailProvider) *DetailHandler { return &DetailHandler{service: service} }

func (h *DetailHandler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Get("/api/v1/events/{eventId}", h.GetEvent)
}

// GetEvent returns a public catalog projection. Detail deliberately has no
// location query: the route does not accept coordinates for profiling or ranking.
func (h *DetailHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeDetailError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}

	eventID, err := uuid.Parse(chi.URLParam(r, "eventId"))
	if err != nil || eventID == uuid.Nil {
		writeDetailError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
		return
	}
	detail, err := h.service.Get(r.Context(), principal.UserID, eventID, nil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeDetailError(w, r, http.StatusNotFound, "NOT_FOUND", "Event not found")
			return
		}
		writeDetailError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, detailResponse(detail))
}

func detailResponse(detail Detail) api.EventDetail {
	response := api.EventDetail{
		CategorySlug:    api.CategorySlug(detail.CategorySlug),
		Currency:        api.EventDetailCurrency(detail.Currency),
		DataProvenance:  api.DataProvenance{Source: detail.Provenance.Source, IsDemo: detail.Provenance.IsDemo, SourceUpdatedAt: nullable.NewNullNullable[time.Time]()},
		DateLabel:       detail.DateLabel,
		Description:     detail.Description,
		DistanceLabel:   nullable.NewNullNullable[string](),
		DistanceM:       nullable.NewNullNullable[int](),
		EndsAt:          nullable.NewNullNullable[time.Time](),
		Id:              detail.ID,
		ImageUrl:        nullable.NewNullNullable[string](),
		Images:          make([]api.EventImage, len(detail.Images)),
		PriceFromMinor:  nullable.NewNullNullable[int](),
		PriceLabel:      detail.PriceLabel,
		Reasons:         make([]api.RecommendationReason, len(detail.Reasons)),
		Saved:           detail.Saved,
		StartsAt:        detail.StartsAt,
		Status:          api.EventDetailStatus(detail.Status),
		Subtitle:        nullable.NewNullNullable[string](),
		TicketAvailable: detail.TicketAvailable,
		Timezone:        detail.Timezone,
		Title:           detail.Title,
		Venue: api.Venue{Id: detail.Venue.ID, Name: detail.Venue.Name, Address: detail.Venue.Address,
			Latitude: nullable.NewNullNullable[float32](), Longitude: nullable.NewNullNullable[float32](), Metro: nullable.NewNullNullable[string](), District: nullable.NewNullNullable[string]()},
		VenueName: detail.VenueName,
		AgeRating: nullable.NewNullNullable[api.EventDetailAgeRating](),
	}
	if detail.Venue.Latitude != nil && detail.Venue.Longitude != nil {
		response.Venue.Latitude = nullable.NewNullableWithValue(float32(*detail.Venue.Latitude))
		response.Venue.Longitude = nullable.NewNullableWithValue(float32(*detail.Venue.Longitude))
	}
	if detail.Subtitle != nil {
		response.Subtitle = nullable.NewNullableWithValue(*detail.Subtitle)
	}
	if detail.DistanceMeters != nil {
		response.DistanceM = nullable.NewNullableWithValue(*detail.DistanceMeters)
	}
	if detail.DistanceLabel != nil {
		response.DistanceLabel = nullable.NewNullableWithValue(*detail.DistanceLabel)
	}
	if detail.PriceFromMinor != nil {
		response.PriceFromMinor = nullable.NewNullableWithValue(*detail.PriceFromMinor)
	}
	if detail.ImageURL != nil {
		response.ImageUrl = nullable.NewNullableWithValue(*detail.ImageURL)
	}
	if detail.EndsAt != nil {
		response.EndsAt = nullable.NewNullableWithValue(*detail.EndsAt)
	}
	if detail.AgeRating != nil {
		response.AgeRating = nullable.NewNullableWithValue(api.EventDetailAgeRating(*detail.AgeRating))
	}
	if detail.Venue.Metro != nil {
		response.Venue.Metro = nullable.NewNullableWithValue(*detail.Venue.Metro)
	}
	if detail.Venue.District != nil {
		response.Venue.District = nullable.NewNullableWithValue(*detail.Venue.District)
	}
	if detail.Provenance.SourceUpdatedAt != nil {
		response.DataProvenance.SourceUpdatedAt = nullable.NewNullableWithValue(*detail.Provenance.SourceUpdatedAt)
	}
	for index, reason := range detail.Reasons {
		response.Reasons[index] = api.RecommendationReason{Code: api.RecommendationReasonCode(reason.Code), Text: reason.Text}
	}
	for index, image := range detail.Images {
		response.Images[index] = api.EventImage{Url: image.URL, Role: api.EventImageRole(image.Role), Width: nullable.NewNullNullable[int](), Height: nullable.NewNullNullable[int]()}
		if image.Width != nil {
			response.Images[index].Width = nullable.NewNullableWithValue(*image.Width)
		}
		if image.Height != nil {
			response.Images[index].Height = nullable.NewNullableWithValue(*image.Height)
		}
	}
	return response
}

func writeDetailError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
