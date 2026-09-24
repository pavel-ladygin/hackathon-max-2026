package discovery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
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
	r.With(authenticate).Get("/api/v1/events/map", h.MapEvents)
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
	filter.Bounds, err = boundsQuery(query)
	if err != nil {
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

func boundsQuery(query map[string][]string) (*Bounds, error) {
	keys := []string{"west", "south", "east", "north"}
	values := make([]float64, 4)
	present := 0
	for i, key := range keys {
		raw, ok := queryValue(query, key)
		if !ok && query[key] != nil {
			return nil, ErrInvalidFilter
		}
		if !ok {
			continue
		}
		present++
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, ErrInvalidFilter
		}
		values[i] = value
	}
	if present == 0 {
		return nil, nil
	}
	if present != len(keys) {
		return nil, ErrInvalidFilter
	}
	bounds := &Bounds{West: values[0], South: values[1], East: values[2], North: values[3]}
	if !validBounds(*bounds) {
		return nil, ErrInvalidFilter
	}
	return bounds, nil
}

// MapEvents returns all matching events represented as cards or world-anchored
// 64px clusters. Paging is performed internally so cluster counts are exact.
func (h *SearchHandler) MapEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeSearchError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	query := r.URL.Query()
	zoomRaw, ok := queryValue(query, "zoom")
	if !ok || query["zoom"] == nil {
		writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event map request")
		return
	}
	zoom, err := strconv.Atoi(zoomRaw)
	if err != nil || zoom < 0 || zoom > 22 {
		writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event map request")
		return
	}
	if query["cursor"] != nil || query["limit"] != nil {
		writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event map request")
		return
	}
	filter, err := searchInput(r.Context(), r, principal.UserID, h.cities, h.service)
	if err != nil {
		writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid event map request")
		return
	}
	if filter.Bounds == nil {
		writeSearchError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Map bounds are required")
		return
	}
	filter.Bounds = expandBoundsToGrid(*filter.Bounds, zoom)
	filter.Limit = maxLimit
	mapSearch, ok := h.service.(interface {
		SearchMapPage(context.Context, SearchFilter) (Page, error)
	})
	if !ok {
		writeSearchError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	groups := make(map[mapCell]mapGroup)
	for {
		page, searchErr := mapSearch.SearchMapPage(r.Context(), filter)
		if searchErr != nil {
			writeSearchError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
			return
		}
		for _, event := range page.Items {
			addMapEvent(groups, event, zoom)
		}
		if page.NextCursor == nil {
			break
		}
		filter.Cursor = page.NextCursor
	}
	items := mapItemsFromGroups(groups, zoom)
	httpapi.WriteJSON(w, http.StatusOK, api.EventMapResponse{Items: items})
}

type mapCell struct{ x, y int }

type mapGroup struct {
	count int
	event Card
}

func addMapEvent(groups map[mapCell]mapGroup, event Card, zoom int) {
	if event.Latitude == nil || event.Longitude == nil {
		return
	}
	worldCells := 4 << zoom
	x := int(math.Floor((*event.Longitude+180)/360*float64(worldCells))) % worldCells
	lat := math.Max(-85.05112878, math.Min(85.05112878, *event.Latitude)) * math.Pi / 180
	y := int(math.Floor((1 - math.Asinh(math.Tan(lat))/math.Pi) / 2 * float64(worldCells)))
	if y < 0 {
		y = 0
	}
	if y >= worldCells {
		y = worldCells - 1
	}
	cell := mapCell{x: x, y: y}
	group := groups[cell]
	group.count++
	if group.count == 1 {
		group.event = event
	} else {
		group.event = Card{}
	}
	groups[cell] = group
}

func clusterMapEvents(events []Card, zoom int) []api.EventMapResponse_Items_Item {
	groups := make(map[mapCell]mapGroup)
	for _, event := range events {
		addMapEvent(groups, event, zoom)
	}
	return mapItemsFromGroups(groups, zoom)
}

func mapItemsFromGroups(groups map[mapCell]mapGroup, zoom int) []api.EventMapResponse_Items_Item {
	worldCells := 4 << zoom
	cells := make([]mapCell, 0, len(groups))
	for cell := range groups {
		cells = append(cells, cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].y == cells[j].y {
			return cells[i].x < cells[j].x
		}
		return cells[i].y < cells[j].y
	})
	items := make([]api.EventMapResponse_Items_Item, 0, len(cells))
	for _, cell := range cells {
		group := groups[cell]
		west, east := float64(cell.x)/float64(worldCells)*360-180, float64(cell.x+1)/float64(worldCells)*360-180
		north := mercatorLatitude(float64(cell.y) / float64(worldCells))
		south := mercatorLatitude(float64(cell.y+1) / float64(worldCells))
		if group.count == 1 {
			card := homeCard(group.event)
			if group.event.Latitude != nil {
				card.Latitude = nullable.NewNullableWithValue(*group.event.Latitude)
			}
			if group.event.Longitude != nil {
				card.Longitude = nullable.NewNullableWithValue(*group.event.Longitude)
			}
			lat, lng := *group.event.Latitude, *group.event.Longitude
			var item api.EventMapResponse_Items_Item
			_ = item.FromEventMapPoint(api.EventMapPoint{Kind: "event", Id: card.Id, Longitude: lng, Latitude: lat, Event: card})
			items = append(items, item)
		} else {
			var item api.EventMapResponse_Items_Item
			_ = item.FromEventMapCluster(api.EventMapCluster{Kind: "cluster", Id: fmt.Sprintf("%d/%d/%d", zoom, cell.x, cell.y), Longitude: (west + east) / 2, Latitude: (north + south) / 2, West: west, South: south, East: east, North: north, Count: group.count})
			items = append(items, item)
		}
	}
	return items
}

func mercatorLatitude(y float64) float64 {
	return math.Atan(math.Sinh(math.Pi*(1-2*y))) * 180 / math.Pi
}

func expandBoundsToGrid(bounds Bounds, zoom int) *Bounds {
	worldCells := 4 << zoom
	cellX := func(longitude float64) int {
		x := int(math.Floor((longitude + 180) / 360 * float64(worldCells)))
		if x == worldCells {
			x--
		}
		if x < 0 {
			x = 0
		}
		if x >= worldCells {
			x = worldCells - 1
		}
		return x
	}
	westCell, eastCell := cellX(bounds.West), cellX(bounds.East)
	west := float64(westCell)/float64(worldCells)*360 - 180
	east := float64(eastCell+1)/float64(worldCells)*360 - 180
	mercatorY := func(latitude float64) float64 {
		lat := math.Max(-85.05112878, math.Min(85.05112878, latitude)) * math.Pi / 180
		return (1 - math.Asinh(math.Tan(lat))/math.Pi) / 2 * float64(worldCells)
	}
	topCell := int(math.Floor(mercatorY(bounds.North)))
	bottomCell := int(math.Floor(mercatorY(bounds.South)))
	if topCell < 0 {
		topCell = 0
	}
	if bottomCell >= worldCells {
		bottomCell = worldCells - 1
	}
	north := mercatorLatitude(float64(topCell) / float64(worldCells))
	south := mercatorLatitude(float64(bottomCell+1) / float64(worldCells))
	return &Bounds{West: west, South: south, East: east, North: north}
}

func ptr(value float64) *float64 { return &value }

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
		if card.Latitude != nil && card.Longitude != nil {
			response.Items[index].Latitude = nullable.NewNullableWithValue(*card.Latitude)
			response.Items[index].Longitude = nullable.NewNullableWithValue(*card.Longitude)
		}
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
