package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCursorCodecRejectsTamperingAndDifferentFilter(t *testing.T) {
	codec, err := NewCursorCodec([]byte("discovery-cursor-test-key-123456"))
	if err != nil {
		t.Fatal(err)
	}
	cursor := Cursor{StartsAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC), EventID: uuid.New(), FilterHash: "filter-a"}
	encoded, err := codec.Encode(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := codec.Decode(encoded, "filter-a"); err != nil || decoded.EventID != cursor.EventID || !decoded.StartsAt.Equal(cursor.StartsAt) {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if _, err := codec.Decode(encoded+"x", "filter-a"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	if _, err := codec.Decode(encoded, "filter-b"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("different filter error = %v", err)
	}
}

func TestCursorCodecRejectsMalformedAndOversizedValues(t *testing.T) {
	codec, err := NewCursorCodec([]byte("discovery-cursor-test-key-123456"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", ".", "payload", "a.b.c", strings.Repeat("A", 8192) + ".sig"} {
		if _, err := codec.Decode(value, "filter"); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("Decode(%q) error = %v, want ErrInvalidCursor", value, err)
		}
	}
}

func TestNormalizeFilterPreservesZeroPriceAndRequiresGeoPair(t *testing.T) {
	zero := int32(0)
	filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), PriceMaxMinor: &zero}
	if err := normalizeFilter(&filter); err != nil {
		t.Fatal(err)
	}
	if filter.PriceMaxMinor == nil || *filter.PriceMaxMinor != 0 {
		t.Fatalf("zero price filter was lost: %+v", filter.PriceMaxMinor)
	}
	distance := int32(500)
	if err := normalizeFilter(&SearchFilter{UserID: uuid.New(), CityID: uuid.New(), DistanceMeters: &distance}); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("distance without location error = %v", err)
	}
	if err := normalizeFilter(&SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Location: &Location{Latitude: 55.75, Longitude: 37.61}}); err != nil {
		t.Fatalf("location without radius error = %v", err)
	}
}

func TestNormalizeFilterRejectsMissingUserAndInvalidCursorKey(t *testing.T) {
	if err := normalizeFilter(&SearchFilter{CityID: uuid.New()}); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("missing user error = %v", err)
	}
	if err := normalizeFilter(&SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Cursor: &Cursor{FilterHash: "filter"}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("incomplete cursor error = %v", err)
	}
}

func TestBoundsValidationAndCursorBinding(t *testing.T) {
	for _, bounds := range []Bounds{{West: 170, East: -170, South: -10, North: 10}, {West: -180, East: 180, South: -90, North: 90}} {
		if err := normalizeFilter(&SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Bounds: &bounds}); err != nil {
			t.Errorf("valid bounds %+v rejected: %v", bounds, err)
		}
	}
	for _, bounds := range []Bounds{{West: 1, East: 1, South: -1, North: 1}, {West: -181, East: 1, South: -1, North: 1}, {West: 1, East: 2, South: 2, North: 1}} {
		if err := normalizeFilter(&SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Bounds: &bounds}); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("invalid bounds %+v error = %v", bounds, err)
		}
	}
	codec, _ := NewCursorCodec([]byte("discovery-cursor-test-key-123456"))
	filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Bounds: &Bounds{West: 37, South: 55, East: 38, North: 56}}
	if err := normalizeFilter(&filter); err != nil {
		t.Fatal(err)
	}
	cursor := Cursor{StartsAt: time.Now(), EventID: uuid.New(), FilterHash: hashFilter(filter)}
	encoded, err := codec.Encode(cursor)
	if err != nil {
		t.Fatal(err)
	}
	filter.Bounds = &Bounds{West: 38, South: 55, East: 39, North: 56}
	if _, err := codec.Decode(encoded, hashFilter(filter)); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor accepted changed bounds: %v", err)
	}
}

func TestNormalizeFilterAppliesLimitBoundsWithoutClamping(t *testing.T) {
	base := func(limit int) SearchFilter {
		return SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Limit: limit}
	}

	zero := base(0)
	if err := normalizeFilter(&zero); err != nil {
		t.Fatal(err)
	}
	if zero.Limit != defaultLimit {
		t.Fatalf("zero limit = %d, want %d", zero.Limit, defaultLimit)
	}
	for _, limit := range []int{1, maxLimit} {
		filter := base(limit)
		if err := normalizeFilter(&filter); err != nil || filter.Limit != limit {
			t.Errorf("limit %d normalized to %d with error %v", limit, filter.Limit, err)
		}
	}
	for _, limit := range []int{-1, maxLimit + 1} {
		filter := base(limit)
		if err := normalizeFilter(&filter); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("limit %d error = %v, want ErrInvalidFilter", limit, err)
		}
	}
}

func TestNormalizeFilterValidatesCategorySlugEnum(t *testing.T) {
	valid := []string{"concerts", "cinema", "theatre", "standup", "exhibitions", "sports", "food", "parties", "festivals", "walks", "other"}
	for _, category := range valid {
		filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), CategorySlugs: []string{" " + strings.ToUpper(category) + " "}}
		if err := normalizeFilter(&filter); err != nil {
			t.Errorf("valid category %q rejected: %v", category, err)
		}
	}
	for _, category := range []string{"unknown", "concert", "music"} {
		filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), CategorySlugs: []string{category}}
		if err := normalizeFilter(&filter); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("invalid category %q error = %v, want ErrInvalidFilter", category, err)
		}
	}
}

func TestNormalizeFilterCountsQueryRunesForMaxLength(t *testing.T) {
	accepted := strings.Repeat("я", 120)
	filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Query: &accepted}
	if err := normalizeFilter(&filter); err != nil {
		t.Fatalf("120-rune query rejected: %v", err)
	}
	rejected := strings.Repeat("я", 121)
	filter = SearchFilter{UserID: uuid.New(), CityID: uuid.New(), Query: &rejected}
	if err := normalizeFilter(&filter); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("121-rune query error = %v, want ErrInvalidFilter", err)
	}
}

func TestDateArgumentKeepsCalendarDayAcrossTimezone(t *testing.T) {
	value := time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("west", -7*60*60))
	got := dateArgument(&value)
	if got == nil || *got != "2026-09-20" {
		t.Fatalf("date argument = %v", got)
	}
}

func TestFilterHashUsesTheSameCalendarDateAsSQLArgument(t *testing.T) {
	city, user := uuid.New(), uuid.New()
	west := time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("west", -7*60*60))
	east := time.Date(2026, 9, 20, 0, 0, 0, 0, time.FixedZone("east", 5*60*60))
	filter := SearchFilter{UserID: user, CityID: city, DateFrom: &west}
	if got := hashFilter(filter); got != hashFilter(SearchFilter{UserID: user, CityID: city, DateFrom: &east}) {
		t.Fatalf("calendar-equivalent date hash changed: %q", got)
	}
	if date := dateArgument(filter.DateFrom); date == nil || *date != "2026-09-20" {
		t.Fatalf("SQL date = %v", date)
	}
}

func TestTimeSlotMidnightIsNight(t *testing.T) {
	for _, hour := range []int{0, 1, 5, 22, 23} {
		if got := timeSlot(hour); got != "night" {
			t.Fatalf("hour %d slot = %q", hour, got)
		}
	}
	for hour, want := range map[int]string{6: "morning", 12: "day", 17: "evening"} {
		if got := timeSlot(hour); got != want {
			t.Fatalf("hour %d slot = %q, want %q", hour, got, want)
		}
	}
}

func TestPriceLabelPreservesNullableAndFreePrices(t *testing.T) {
	if got := priceLabel(pgtype.Int4{}); got != "Цена уточняется" {
		t.Fatalf("null price label = %q", got)
	}
	if got := priceLabel(pgtype.Int4{Int32: 0, Valid: true}); got != "Бесплатно" {
		t.Fatalf("zero price label = %q", got)
	}
	if got := priceLabel(pgtype.Int4{Int32: 180000, Valid: true}); got != "от 1 800 ₽" {
		t.Fatalf("paid price label = %q", got)
	}
	if got := priceLabel(pgtype.Int4{Int32: 180050, Valid: true}); got != "от 1 800,50 ₽" {
		t.Fatalf("paid price label with kopecks = %q", got)
	}
}

func TestServiceBindsNextCursorToNormalizedFilter(t *testing.T) {
	codec, err := NewCursorCodec([]byte("discovery-cursor-test-key-123456"))
	if err != nil {
		t.Fatal(err)
	}
	startsAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	next := Cursor{StartsAt: startsAt, EventID: uuid.New()}
	reader := fakeReader{page: Page{Items: []Card{{ID: next.EventID, StartsAt: startsAt}}, NextCursor: &next}}
	service := NewService(&reader, codec)
	filter := SearchFilter{UserID: uuid.New(), CityID: uuid.New(), CategorySlugs: []string{" concerts ", "concerts"}}
	page, err := service.Search(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil || page.NextCursor.FilterHash == "" {
		t.Fatalf("next cursor = %+v", page.NextCursor)
	}
	encoded, err := service.EncodeNextCursor(*page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := service.DecodeCursor(encoded, SearchFilter{UserID: filter.UserID, CityID: filter.CityID, CategorySlugs: []string{"concerts"}})
	if err != nil || decoded.EventID != next.EventID {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if _, err := service.DecodeCursor(encoded, SearchFilter{UserID: filter.UserID, CityID: filter.CityID, CategorySlugs: []string{"theatre"}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor accepted different filter: %v", err)
	}
	if _, err := service.DecodeCursor(encoded, SearchFilter{UserID: uuid.New(), CityID: filter.CityID, CategorySlugs: []string{"concerts"}}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor accepted a different user: %v", err)
	}
}

type fakeReader struct{ page Page }

func (f *fakeReader) Search(context.Context, SearchFilter) (Page, error) { return f.page, nil }
func (f *fakeReader) Get(context.Context, uuid.UUID, uuid.UUID, *Location) (Detail, error) {
	return Detail{}, nil
}
