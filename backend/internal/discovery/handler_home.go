package discovery

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

// HomeFeedProvider is the Home transport boundary.
type HomeFeedProvider interface {
	Home(context.Context, HomeInput) (HomeFeed, error)
}

// HomeHandler exposes the authenticated home-feed route.
type HomeHandler struct {
	service       HomeFeedProvider
	operationSink behavior.OperationEventSink
}

func NewHomeHandler(service HomeFeedProvider) *HomeHandler { return &HomeHandler{service: service} }

func (h *HomeHandler) EnableOperationTelemetry(sink behavior.OperationEventSink) {
	h.operationSink = sink
}

func (h *HomeHandler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate, behavior.OperationMiddleware("feed_load", h.operationSink)).Get("/api/v1/feed/home", h.GetHome)
}

func (h *HomeHandler) GetHome(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeHomeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	input, err := homeInput(r, principal.UserID)
	if err != nil {
		writeHomeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid home feed request")
		return
	}
	feed, err := h.service.Home(r.Context(), input)
	if err != nil {
		if errors.Is(err, ErrInvalidHome) || errors.Is(err, ErrInvalidFilter) {
			writeHomeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid home feed request")
			return
		}
		writeHomeError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, homeResponse(feed))
}

func homeInput(r *http.Request, userID uuid.UUID) (HomeInput, error) {
	query := r.URL.Query()
	input := HomeInput{UserID: userID}
	if raw := query.Get("city_id"); raw != "" {
		cityID, err := uuid.Parse(raw)
		if err != nil || cityID == uuid.Nil {
			return HomeInput{}, ErrInvalidHome
		}
		input.CityID = &cityID
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxLimit {
			return HomeInput{}, ErrInvalidHome
		}
		input.Limit = limit
	}
	lat, hasLat := query["lat"]
	lng, hasLng := query["lng"]
	if hasLat != hasLng || (hasLat && (len(lat) != 1 || len(lng) != 1)) {
		return HomeInput{}, ErrInvalidHome
	}
	if hasLat {
		latitude, err := strconv.ParseFloat(lat[0], 64)
		if err != nil {
			return HomeInput{}, ErrInvalidHome
		}
		longitude, err := strconv.ParseFloat(lng[0], 64)
		if err != nil || !validLocation(Location{Latitude: latitude, Longitude: longitude}) {
			return HomeInput{}, ErrInvalidHome
		}
		input.Location = &Location{Latitude: latitude, Longitude: longitude}
	}
	return input, nil
}

func homeResponse(feed HomeFeed) api.HomeFeedResponse {
	response := api.HomeFeedResponse{FeedId: feed.ID, GeneratedAt: feed.GeneratedAt, ActiveRoom: nullable.NewNullNullable[api.RoomSummary](), RoomClosedNotice: nullable.NewNullNullable[api.RoomClosedNotice](), Sections: make([]struct {
		Items []api.EventCard                  `json:"items"`
		Title string                           `json:"title"`
		Type  api.HomeFeedResponseSectionsType `json:"type"`
	}, 0, len(feed.Sections))}
	if feed.ActiveRoom != nil {
		response.ActiveRoom = nullable.NewNullableWithValue(api.RoomSummary{
			Id: feed.ActiveRoom.ID, Name: feed.ActiveRoom.Name, CityId: feed.ActiveRoom.CityID, State: api.RoomState(feed.ActiveRoom.State),
		})
	}
	if feed.RoomClosedNotice != nil {
		n := feed.RoomClosedNotice
		response.RoomClosedNotice = nullable.NewNullableWithValue(api.RoomClosedNotice{
			RoomId: n.RoomID, RoomName: n.RoomName,
			ClosedBy: api.ClosedBy{Id: n.ClosedByID, DisplayName: n.ClosedByDisplayName}, ClosedAt: n.ClosedAt,
		})
	}
	for _, section := range feed.Sections {
		item := struct {
			Items []api.EventCard                  `json:"items"`
			Title string                           `json:"title"`
			Type  api.HomeFeedResponseSectionsType `json:"type"`
		}{Items: make([]api.EventCard, len(section.Items)), Title: section.Title, Type: api.HomeFeedResponseSectionsType(section.Type)}
		for index, card := range section.Items {
			item.Items[index] = homeCard(card)
		}
		response.Sections = append(response.Sections, item)
	}
	return response
}

func homeCard(card Card) api.EventCard {
	result := api.EventCard{Id: card.ID, Title: card.Title, CategorySlug: api.CategorySlug(card.CategorySlug), StartsAt: card.StartsAt, Timezone: card.Timezone, DateLabel: card.DateLabel, VenueName: card.VenueName, Currency: api.EventCardCurrency(card.Currency), PriceLabel: card.PriceLabel, Saved: card.Saved, Reasons: make([]api.RecommendationReason, len(card.Reasons)), ImageUrl: nullable.NewNullNullable[string](), PriceFromMinor: nullable.NewNullNullable[int](), DistanceM: nullable.NewNullNullable[int](), DistanceLabel: nullable.NewNullNullable[string](), Subtitle: nullable.NewNullNullable[string]()}
	if card.ImageURL != nil {
		result.ImageUrl = nullable.NewNullableWithValue(*card.ImageURL)
	}
	if card.PriceFromMinor != nil {
		result.PriceFromMinor = nullable.NewNullableWithValue(*card.PriceFromMinor)
	}
	if card.DistanceMeters != nil {
		result.DistanceM = nullable.NewNullableWithValue(*card.DistanceMeters)
	}
	if card.DistanceLabel != nil {
		result.DistanceLabel = nullable.NewNullableWithValue(*card.DistanceLabel)
	}
	if card.Subtitle != nil {
		result.Subtitle = nullable.NewNullableWithValue(*card.Subtitle)
	}
	for index, reason := range card.Reasons {
		result.Reasons[index] = api.RecommendationReason{Code: api.RecommendationReasonCode(reason.Code), Text: reason.Text}
	}
	return result
}

func writeHomeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
