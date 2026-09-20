package discovery

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

// SearchProvider is the discovery capability used by the search transport.
type SearchProvider interface {
	Search(context.Context, SearchFilter) (Page, error)
	EncodeNextCursor(Cursor) (string, error)
	DecodeCursor(string, SearchFilter) (Cursor, error)
}

// SearchCityReader resolves the authenticated user's profile city when omitted.
type SearchCityReader interface {
	UserCity(context.Context, uuid.UUID) (uuid.UUID, error)
}

// SearchHandler exposes the authenticated event-catalog search route.
type SearchHandler struct {
	service SearchProvider
	cities  SearchCityReader
}

func NewSearchHandler(service SearchProvider, cities SearchCityReader) *SearchHandler {
	return &SearchHandler{service: service, cities: cities}
}

func (h *SearchHandler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Get("/api/v1/events/search", h.SearchEvents)
}

func (h *SearchHandler) SearchEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeSearchError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	filter, err := searchInput(r.Context(), r, principal.UserID, h.cities, h.service)
	if err != nil {
		if errors.Is(err, ErrInvalidFilter) || errors.Is(err, ErrInvalidCursor) || errors.Is(err, pgx.ErrNoRows) {
			writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event search request")
			return
		}
		writeSearchError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	page, err := h.service.Search(r.Context(), filter)
	if err != nil {
		if errors.Is(err, ErrInvalidFilter) || errors.Is(err, ErrInvalidCursor) {
			writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event search request")
			return
		}
		writeSearchError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	response, err := searchResponse(page, filter, h.service)
	if err != nil {
		writeSearchError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func searchInput(ctx context.Context, r *http.Request, userID uuid.UUID, cities SearchCityReader, service SearchProvider) (SearchFilter, error) {
	query := r.URL.Query()
	filter := SearchFilter{UserID: userID}

	city, err := uuidQuery(query, "city_id")
	if err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if city != nil {
		filter.CityID = *city
	} else {
		filter.CityID, err = cities.UserCity(ctx, userID)
		if err != nil {
			return SearchFilter{}, err
		}
		if filter.CityID == uuid.Nil {
			return SearchFilter{}, ErrInvalidFilter
		}
	}

	if filter.Query, err = stringQuery(query, "q"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.DateFrom, err = dateQuery(query, "date_from"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.DateTo, err = dateQuery(query, "date_to"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.DayTypes, err = csvQuery(query, "day_types"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.TimeSlots, err = csvQuery(query, "time_slots"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.CategorySlugs, err = csvQuery(query, "category_slugs"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.PriceMaxMinor, err = int32Query(query, "price_max_minor"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.DistanceMeters, err = int32Query(query, "distance_m"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.FreeOnly, err = boolQuery(query, "free_only"); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.Limit, err = limitQuery(query); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.Location, err = locationQuery(query); err != nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if filter.DistanceMeters != nil && filter.Location == nil {
		return SearchFilter{}, ErrInvalidFilter
	}
	if err := normalizeFilter(&filter); err != nil {
		return SearchFilter{}, err
	}
	if raw, ok := queryValue(query, "cursor"); !ok && query["cursor"] != nil {
		return SearchFilter{}, ErrInvalidCursor
	} else if !ok {
		// No cursor is valid.
	} else if raw == "" || len(raw) > maxCursorLength {
		return SearchFilter{}, ErrInvalidCursor
	} else {
		cursor, err := service.DecodeCursor(raw, filter)
		if err != nil {
			return SearchFilter{}, ErrInvalidCursor
		}
		filter.Cursor = &cursor
	}
	return filter, nil
}

func queryValue(query map[string][]string, key string) (string, bool) {
	values, ok := query[key]
	returnValue := ""
	if ok && len(values) == 1 {
		returnValue = values[0]
	} else if ok {
		return "", false
	}
	return returnValue, ok
}

func stringQuery(query map[string][]string, key string) (*string, error) {
	value, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return nil, ErrInvalidFilter
	}
	if !ok {
		return nil, nil
	}
	return &value, nil
}

func uuidQuery(query map[string][]string, key string) (*uuid.UUID, error) {
	raw, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return nil, ErrInvalidFilter
	}
	if !ok {
		return nil, nil
	}
	if raw == "" {
		return nil, ErrInvalidFilter
	}
	value, err := uuid.Parse(raw)
	if err != nil || value == uuid.Nil {
		return nil, ErrInvalidFilter
	}
	return &value, nil
}

func dateQuery(query map[string][]string, key string) (*time.Time, error) {
	raw, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return nil, ErrInvalidFilter
	}
	if !ok {
		return nil, nil
	}
	if raw == "" {
		return nil, ErrInvalidFilter
	}
	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, ErrInvalidFilter
	}
	return &value, nil
}

func csvQuery(query map[string][]string, key string) ([]string, error) {
	raw, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return nil, ErrInvalidFilter
	}
	if !ok {
		return nil, nil
	}
	if raw == "" {
		return nil, ErrInvalidFilter
	}
	return strings.Split(raw, ","), nil
}

func int32Query(query map[string][]string, key string) (*int32, error) {
	raw, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return nil, ErrInvalidFilter
	}
	if !ok {
		return nil, nil
	}
	if raw == "" {
		return nil, ErrInvalidFilter
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil, ErrInvalidFilter
	}
	result := int32(value)
	return &result, nil
}

func boolQuery(query map[string][]string, key string) (bool, error) {
	raw, ok := queryValue(query, key)
	if !ok && query[key] != nil {
		return false, ErrInvalidFilter
	}
	if !ok {
		return false, nil
	}
	if raw == "" {
		return false, ErrInvalidFilter
	}
	if raw == "true" {
		return true, nil
	}
	if raw == "false" {
		return false, nil
	}
	return false, ErrInvalidFilter
}

func limitQuery(query map[string][]string) (int, error) {
	raw, ok := queryValue(query, "limit")
	if !ok && query["limit"] != nil {
		return 0, ErrInvalidFilter
	}
	if !ok {
		return defaultLimit, nil
	}
	if raw == "" {
		return 0, ErrInvalidFilter
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxLimit {
		return 0, ErrInvalidFilter
	}
	return value, nil
}

func locationQuery(query map[string][]string) (*Location, error) {
	lat, hasLat := query["lat"]
	lng, hasLng := query["lng"]
	if hasLat != hasLng || (hasLat && (len(lat) != 1 || len(lng) != 1)) {
		return nil, ErrInvalidFilter
	}
	if !hasLat {
		return nil, nil
	}
	latitude, err := strconv.ParseFloat(lat[0], 64)
	if err != nil {
		return nil, ErrInvalidFilter
	}
	longitude, err := strconv.ParseFloat(lng[0], 64)
	if err != nil || !validLocation(Location{Latitude: latitude, Longitude: longitude}) {
		return nil, ErrInvalidFilter
	}
	return &Location{Latitude: latitude, Longitude: longitude}, nil
}

func searchResponse(page Page, filter SearchFilter, service SearchProvider) (api.EventSearchResponse, error) {
	response := api.EventSearchResponse{Items: make([]api.EventCard, len(page.Items)), AppliedFilters: appliedFilters(filter), TotalEstimate: page.Total, NextCursor: nullable.NewNullNullable[string]()}
	for index, card := range page.Items {
		response.Items[index] = homeCard(card)
	}
	if page.NextCursor != nil {
		encoded, err := service.EncodeNextCursor(*page.NextCursor)
		if err != nil {
			return api.EventSearchResponse{}, err
		}
		response.NextCursor = nullable.NewNullableWithValue(encoded)
	}
	return response, nil
}

func appliedFilters(filter SearchFilter) map[string]interface{} {
	result := map[string]interface{}{"city_id": filter.CityID.String(), "day_types": filter.DayTypes, "time_slots": filter.TimeSlots, "category_slugs": filter.CategorySlugs, "free_only": filter.FreeOnly}
	if filter.Query != nil {
		result["q"] = *filter.Query
	}
	if filter.DateFrom != nil {
		result["date_from"] = filter.DateFrom.Format("2006-01-02")
	}
	if filter.DateTo != nil {
		result["date_to"] = filter.DateTo.Format("2006-01-02")
	}
	if filter.PriceMaxMinor != nil {
		result["price_max_minor"] = *filter.PriceMaxMinor
	}
	if filter.DistanceMeters != nil {
		result["distance_m"] = *filter.DistanceMeters
	}
	return result
}

func writeSearchError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
