package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrInvalidFilter = errors.New("invalid discovery filter")

type Reader interface {
	Search(context.Context, SearchFilter) (Page, error)
	Get(context.Context, uuid.UUID, uuid.UUID, *Location) (Detail, error)
}

// Service normalizes input and owns cursor/filter compatibility. It has no HTTP
// dependency so future feed, search and detail handlers can share it.
type Service struct {
	repository Reader
	codec      *CursorCodec
}

func NewService(repository Reader, codec *CursorCodec) *Service {
	return &Service{repository: repository, codec: codec}
}

func (s *Service) Search(ctx context.Context, filter SearchFilter) (Page, error) {
	if err := normalizeFilter(&filter); err != nil {
		return Page{}, err
	}
	filterHash := hashFilter(filter)
	if filter.Cursor != nil && filter.Cursor.FilterHash != filterHash {
		return Page{}, ErrInvalidCursor
	}
	page, err := s.repository.Search(ctx, filter)
	if err != nil || page.NextCursor == nil {
		return page, err
	}
	page.NextCursor.FilterHash = filterHash
	return page, nil
}

// SearchMapPage shares search validation and cursor compatibility while using
// the repository's map-specific path that omits the unused total count.
func (s *Service) SearchMapPage(ctx context.Context, filter SearchFilter) (Page, error) {
	if err := normalizeFilter(&filter); err != nil {
		return Page{}, err
	}
	filterHash := hashFilter(filter)
	if filter.Cursor != nil && filter.Cursor.FilterHash != filterHash {
		return Page{}, ErrInvalidCursor
	}
	reader, ok := s.repository.(interface {
		SearchMapPage(context.Context, SearchFilter) (Page, error)
	})
	if !ok {
		return Page{}, errors.New("discovery repository does not support map paging")
	}
	page, err := reader.SearchMapPage(ctx, filter)
	if err == nil && page.NextCursor != nil {
		page.NextCursor.FilterHash = filterHash
	}
	return page, err
}

func (s *Service) EncodeNextCursor(cursor Cursor) (string, error) { return s.codec.Encode(cursor) }

func (s *Service) DecodeCursor(value string, filter SearchFilter) (Cursor, error) {
	if err := normalizeFilter(&filter); err != nil {
		return Cursor{}, err
	}
	return s.codec.Decode(value, hashFilter(filter))
}

func (s *Service) Get(ctx context.Context, userID, eventID uuid.UUID, location *Location) (Detail, error) {
	if userID == uuid.Nil || eventID == uuid.Nil || (location != nil && !validLocation(*location)) {
		return Detail{}, ErrInvalidFilter
	}
	return s.repository.Get(ctx, userID, eventID, location)
}

func normalizeFilter(filter *SearchFilter) error {
	if filter.UserID == uuid.Nil || filter.CityID == uuid.Nil || filter.Limit < 0 || filter.Limit > maxLimit || filter.PriceMaxMinor != nil && *filter.PriceMaxMinor < 0 ||
		filter.DistanceMeters != nil && (*filter.DistanceMeters < 100 || *filter.DistanceMeters > 50_000) ||
		filter.Location != nil && !validLocation(*filter.Location) || filter.DistanceMeters != nil && filter.Location == nil {
		return ErrInvalidFilter
	}
	if filter.Bounds != nil && !validBounds(*filter.Bounds) {
		return ErrInvalidFilter
	}
	if filter.Cursor != nil && (filter.Cursor.StartsAt.IsZero() || filter.Cursor.EventID == uuid.Nil || filter.Cursor.FilterHash == "") {
		return ErrInvalidCursor
	}
	if filter.DateFrom != nil && filter.DateTo != nil && filter.DateFrom.After(*filter.DateTo) {
		return ErrInvalidFilter
	}
	if filter.Limit == 0 {
		filter.Limit = defaultLimit
	}
	var ok bool
	if filter.DayTypes, ok = normalizedSet(filter.DayTypes, "weekday", "weekend"); !ok {
		return ErrInvalidFilter
	}
	if filter.TimeSlots, ok = normalizedSet(filter.TimeSlots, "morning", "day", "evening", "night"); !ok {
		return ErrInvalidFilter
	}
	if filter.CategorySlugs, ok = normalizedSet(filter.CategorySlugs,
		"concerts", "cinema", "theatre", "standup", "exhibitions", "sports", "food", "parties", "festivals", "walks", "other"); !ok {
		return ErrInvalidFilter
	}
	if filter.Query != nil {
		query := strings.TrimSpace(*filter.Query)
		if utf8.RuneCountInString(query) > 120 {
			return ErrInvalidFilter
		}
		filter.Query = &query
	}
	return nil
}

func validLocation(location Location) bool {
	return !math.IsNaN(location.Latitude) && !math.IsNaN(location.Longitude) && !math.IsInf(location.Latitude, 0) && !math.IsInf(location.Longitude, 0) && location.Latitude >= -90 && location.Latitude <= 90 && location.Longitude >= -180 && location.Longitude <= 180
}

func validBounds(b Bounds) bool {
	return !math.IsNaN(b.West) && !math.IsNaN(b.South) && !math.IsNaN(b.East) && !math.IsNaN(b.North) &&
		!math.IsInf(b.West, 0) && !math.IsInf(b.South, 0) && !math.IsInf(b.East, 0) && !math.IsInf(b.North, 0) &&
		b.West >= -180 && b.West <= 180 && b.East >= -180 && b.East <= 180 && b.West != b.East &&
		b.South >= -90 && b.North <= 90 && b.South < b.North
}

func normalizedSet(values []string, allowed ...string) ([]string, bool) {
	permitted := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		permitted[value] = true
	}
	result := normalizedStrings(values)
	for _, value := range result {
		if !permitted[value] {
			return nil, false
		}
	}
	return result, true
}

func normalizedStrings(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// timeSlot mirrors the PostgreSQL CASE used by discovery.sql.
func timeSlot(hour int) string {
	switch {
	case hour >= 6 && hour < 12:
		return "morning"
	case hour >= 12 && hour < 17:
		return "day"
	case hour >= 17 && hour < 22:
		return "evening"
	default:
		return "night"
	}
}

func hashFilter(filter SearchFilter) string {
	type fingerprint struct {
		User, City, Query       string
		DateFrom, DateTo        string
		Days, Slots, Categories []string
		Price                   *int32
		Free                    bool
		Location                *Location
		Distance                *int32
		Bounds                  *Bounds
	}
	item := fingerprint{User: filter.UserID.String(), City: filter.CityID.String(), Days: filter.DayTypes, Slots: filter.TimeSlots, Categories: filter.CategorySlugs, Price: filter.PriceMaxMinor, Free: filter.FreeOnly, Location: filter.Location, Distance: filter.DistanceMeters}
	item.Bounds = filter.Bounds
	if filter.Query != nil {
		item.Query = *filter.Query
	}
	if filter.DateFrom != nil {
		item.DateFrom = *dateArgument(filter.DateFrom)
	}
	if filter.DateTo != nil {
		item.DateTo = *dateArgument(filter.DateTo)
	}
	encoded, _ := json.Marshal(item)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
