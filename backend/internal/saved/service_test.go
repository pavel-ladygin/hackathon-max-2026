package saved

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestServiceListRejectsInvalidScopeAndPagination(t *testing.T) {
	svc := &Service{}
	for name, input := range map[string]ListInput{
		"missing user":   {Tab: TabSaved, Limit: 20},
		"unknown tab":    {Tab: Tab("other"), Limit: 20},
		"zero limit":     {Tab: TabSaved, Limit: 0},
		"negative limit": {Tab: TabSaved, Limit: -1},
		"over max limit": {Tab: TabSaved, Limit: maxLimit + 1},
	} {
		t.Run(name, func(t *testing.T) {
			inputUser := uuid.New()
			if name == "missing user" {
				inputUser = uuid.Nil
			}
			_, err := svc.List(t.Context(), inputUser, input)
			if err != ErrInvalid {
				t.Fatalf("List error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestServiceSetRejectsNilIdentitiesBeforeDatabaseAccess(t *testing.T) {
	svc := &Service{}
	for name, pair := range map[string][2]uuid.UUID{
		"nil user":  {uuid.Nil, uuid.New()},
		"nil event": {uuid.New(), uuid.Nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Set(t.Context(), pair[0], pair[1], true)
			if err != ErrInvalid {
				t.Fatalf("Set error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCardMapsNullableFieldsAndPublicLabels(t *testing.T) {
	eventID := uuid.New()
	starts := time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC)
	cardWithNulls := card(eventID, "Event", pgtype.Text{}, "concerts", pgtype.Timestamptz{Time: starts, Valid: true}, "UTC", "Venue", pgtype.Int4{}, "RUB", "")
	if cardWithNulls.Subtitle != nil || cardWithNulls.PriceFromMinor != nil || cardWithNulls.ImageURL != nil {
		t.Fatalf("nullable fields = %#v; want nils", cardWithNulls)
	}
	if cardWithNulls.PriceLabel != "Цена уточняется" || cardWithNulls.DateLabel != "1 октября, 16:30" {
		t.Fatalf("labels = %q, %q", cardWithNulls.PriceLabel, cardWithNulls.DateLabel)
	}

	subtitle, image := "Subtitle", "https://cdn.example.test/card.jpg"
	price := int32(123456)
	cardWithValues := card(eventID, "Event", pgtype.Text{String: subtitle, Valid: true}, "concerts", pgtype.Timestamptz{Time: starts, Valid: true}, "UTC", "Venue", pgtype.Int4{Int32: price, Valid: true}, "RUB", image)
	if cardWithValues.Subtitle == nil || *cardWithValues.Subtitle != subtitle || cardWithValues.ImageURL == nil || *cardWithValues.ImageURL != image || cardWithValues.PriceFromMinor == nil || *cardWithValues.PriceFromMinor != int(price) {
		t.Fatalf("mapped values = %#v", cardWithValues)
	}
	if cardWithValues.PriceLabel != "от 1 234,56 ₽" {
		t.Fatalf("price label = %q", cardWithValues.PriceLabel)
	}
}

func TestPriceLabelBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   pgtype.Int4
		want string
	}{
		{"free", pgtype.Int4{Int32: 0, Valid: true}, "Бесплатно"},
		{"whole rubles", pgtype.Int4{Int32: 50000, Valid: true}, "от 500 ₽"},
		{"kopecks", pgtype.Int4{Int32: 501, Valid: true}, "от 5,01 ₽"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := priceLabel(tc.in); got != tc.want {
				t.Fatalf("priceLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCursorCodecBindsUserAndTabAndRejectsTampering(t *testing.T) {
	codec, err := newCursorCodec([]byte("saved-events-test-cursor-key"))
	if err != nil {
		t.Fatal(err)
	}
	user, event := uuid.New(), uuid.New()
	want := Cursor{At: time.Date(2026, 9, 20, 12, 0, 0, 123, time.UTC), ID: event}
	encoded, err := codec.Encode(user, TabSaved, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(user, TabSaved, encoded)
	if err != nil || !got.At.Equal(want.At) || got.ID != want.ID {
		t.Fatalf("decoded cursor = %#v, err=%v; want %#v", got, err, want)
	}
	for name, decode := range map[string]func() error{
		"different user": func() error { _, err := codec.Decode(uuid.New(), TabSaved, encoded); return err },
		"different tab":  func() error { _, err := codec.Decode(user, TabMatches, encoded); return err },
		"tampered":       func() error { _, err := codec.Decode(user, TabSaved, encoded+"x"); return err },
		"empty":          func() error { _, err := codec.Decode(user, TabSaved, ""); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := decode(); err != ErrInvalid {
				t.Fatalf("Decode error = %v, want ErrInvalid", err)
			}
		})
	}
}
