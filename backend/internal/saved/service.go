// Package saved owns the caller-scoped saved-events HTTP capability.
package saved

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

var (
	ErrInvalid  = errors.New("invalid saved events request")
	ErrNotFound = errors.New("event not found")
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

type Tab string

const (
	TabSaved   Tab = "saved"
	TabMatches Tab = "matches"
)

type Cursor struct {
	At time.Time
	ID uuid.UUID
}
type ListInput struct {
	Tab    Tab
	Limit  int
	Cursor *Cursor
}
type State struct {
	EventID uuid.UUID
	Saved   bool
	SavedAt *time.Time
}
type Card struct {
	ID                             uuid.UUID
	Title                          string
	Subtitle                       *string
	CategorySlug                   string
	StartsAt                       time.Time
	Timezone, DateLabel, VenueName string
	PriceFromMinor                 *int
	Currency, PriceLabel           string
	ImageURL                       *string
	Saved                          bool
}
type Participant struct {
	ID          uuid.UUID
	DisplayName string
	AvatarURL   *string
	Role        string
	IntentReady bool
}
type Item struct {
	Event   Card
	SavedAt *time.Time
	Match   *Match
}
type Match struct {
	ID, RoomID, EventID uuid.UUID
	MatchedAt           time.Time
	Participants        []Participant
}
type Page struct {
	Items      []Item
	NextCursor *Cursor
}

type Service struct {
	db       *store.Pool
	cursors  *cursorCodec
	recorder contracts.BehaviorRecorder
	now      func() time.Time
}

func NewService(db *store.Pool, recorder contracts.BehaviorRecorder, key []byte) (*Service, error) {
	if db == nil {
		return nil, errors.New("saved service database is required")
	}
	if recorder == nil {
		return nil, errors.New("saved service behavior recorder is required")
	}
	codec, err := newCursorCodec(key)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, cursors: codec, recorder: recorder, now: time.Now}, nil
}

func (s *Service) Set(ctx context.Context, userID, eventID uuid.UUID, saved bool) (State, error) {
	if userID == uuid.Nil || eventID == uuid.Nil {
		return State{}, ErrInvalid
	}
	var result State
	err := s.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		exists, err := q.SavedEventExists(ctx, eventID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		result = State{EventID: eventID, Saved: saved}
		if !saved {
			rows, err := q.DeleteSavedEvent(ctx, platform.DeleteSavedEventParams{UserID: userID, EventID: eventID})
			if err != nil || rows == 0 {
				return err
			}
			return s.recorder.Record(ctx, tx, contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: userID, Type: "event_unsaved", EventID: &eventID, RequestID: httpapi.RequestID(ctx), OccurredAt: s.now(), DeduplicationKey: "unsave/" + eventID.String() + "/" + httpapi.RequestID(ctx)})
		}
		rows, err := q.InsertSavedEvent(ctx, platform.InsertSavedEventParams{UserID: userID, EventID: eventID})
		if err != nil {
			return err
		}
		if rows > 0 {
			if err := s.recorder.Record(ctx, tx, contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: userID, Type: "event_saved", EventID: &eventID, RequestID: httpapi.RequestID(ctx), OccurredAt: s.now(), DeduplicationKey: "save/" + eventID.String() + "/" + httpapi.RequestID(ctx)}); err != nil {
				return err
			}
		}
		// Keep the lookup as a separate statement. Under READ COMMITTED this gets
		// a fresh snapshot after a concurrent ON CONFLICT winner commits.
		created, err := q.GetSavedEventCreatedAt(ctx, platform.GetSavedEventCreatedAtParams{UserID: userID, EventID: eventID})
		if err != nil {
			return err
		}
		if !created.Valid {
			return errors.New("saved timestamp missing")
		}
		value := created.Time
		result.SavedAt = &value
		return nil
	})
	return result, err
}
func (s *Service) List(ctx context.Context, userID uuid.UUID, input ListInput) (Page, error) {
	if userID == uuid.Nil || (input.Tab != TabSaved && input.Tab != TabMatches) || input.Limit < 1 || input.Limit > maxLimit || input.Cursor != nil && (input.Cursor.At.IsZero() || input.Cursor.ID == uuid.Nil) {
		return Page{}, ErrInvalid
	}
	if input.Tab == TabSaved {
		return s.listSaved(ctx, userID, input)
	}
	return s.listMatches(ctx, userID, input)
}
func (s *Service) EncodeCursor(userID uuid.UUID, tab Tab, cursor Cursor) (string, error) {
	return s.cursors.Encode(userID, tab, cursor)
}
func (s *Service) DecodeCursor(userID uuid.UUID, tab Tab, value string) (Cursor, error) {
	return s.cursors.Decode(userID, tab, value)
}
func (s *Service) listSaved(ctx context.Context, userID uuid.UUID, input ListInput) (Page, error) {
	arg := platform.ListSavedEventCardsParams{UserID: userID, LimitCount: int32(input.Limit + 1)}
	if input.Cursor != nil {
		arg.CursorSavedAt = pgtype.Timestamptz{Time: input.Cursor.At, Valid: true}
		arg.CursorEventID = pgtype.UUID{Bytes: input.Cursor.ID, Valid: true}
	}
	rows, err := platform.New(s.db).ListSavedEventCards(ctx, arg)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]Item, 0, len(rows))}
	for _, row := range rows {
		at := row.SavedAt.Time
		event := card(row.ID, row.Title, row.Subtitle, row.CategorySlug, row.StartsAt, row.Timezone, row.VenueName, row.PriceFromMinor, row.Currency, row.ImageUrl)
		event.Saved = true
		page.Items = append(page.Items, Item{Event: event, SavedAt: &at})
	}
	if len(page.Items) > input.Limit {
		last := page.Items[input.Limit-1]
		page.NextCursor = &Cursor{At: *last.SavedAt, ID: last.Event.ID}
		page.Items = page.Items[:input.Limit]
	}
	return page, nil
}
func (s *Service) listMatches(ctx context.Context, userID uuid.UUID, input ListInput) (Page, error) {
	arg := platform.ListMatchedEventCardsParams{UserID: userID, LimitCount: int32(input.Limit + 1)}
	if input.Cursor != nil {
		arg.CursorMatchedAt = pgtype.Timestamptz{Time: input.Cursor.At, Valid: true}
		arg.CursorMatchID = pgtype.UUID{Bytes: input.Cursor.ID, Valid: true}
	}
	rows, err := platform.New(s.db).ListMatchedEventCards(ctx, arg)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: []Item{}}
	index := map[uuid.UUID]int{}
	for _, row := range rows {
		position, ok := index[row.MatchID]
		if !ok {
			position = len(page.Items)
			index[row.MatchID] = position
			event := card(row.EventID, row.Title, row.Subtitle, row.CategorySlug, row.StartsAt, row.Timezone, row.VenueName, row.PriceFromMinor, row.Currency, row.ImageUrl)
			event.Saved = row.Saved
			page.Items = append(page.Items, Item{Event: event, Match: &Match{ID: row.MatchID, RoomID: row.RoomID, EventID: row.EventID, MatchedAt: row.MatchedAt.Time, Participants: []Participant{}}})
		}
		participant := Participant{ID: row.ParticipantID, DisplayName: row.DisplayName, Role: row.Role, IntentReady: row.IntentReady}
		if row.AvatarUrl.Valid {
			v := row.AvatarUrl.String
			participant.AvatarURL = &v
		}
		page.Items[position].Match.Participants = append(page.Items[position].Match.Participants, participant)
	}
	if len(page.Items) > input.Limit {
		last := page.Items[input.Limit-1].Match
		page.NextCursor = &Cursor{At: last.MatchedAt, ID: last.ID}
		page.Items = page.Items[:input.Limit]
	}
	return page, nil
}
func card(id uuid.UUID, title string, subtitle pgtype.Text, category string, starts pgtype.Timestamptz, timezone, venue string, price pgtype.Int4, currency, image string) Card {
	result := Card{ID: id, Title: title, CategorySlug: category, StartsAt: starts.Time, Timezone: timezone, DateLabel: dateLabel(starts.Time, timezone), VenueName: venue, Currency: currency, PriceLabel: priceLabel(price)}
	if subtitle.Valid {
		v := subtitle.String
		result.Subtitle = &v
	}
	if price.Valid {
		v := int(price.Int32)
		result.PriceFromMinor = &v
	}
	if image != "" {
		result.ImageURL = &image
	}
	return result
}
func dateLabel(value time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return value.UTC().Format("02.01, 15:04")
	}
	local := value.In(location)
	months := [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	return fmt.Sprintf("%d %s, %02d:%02d", local.Day(), months[local.Month()-1], local.Hour(), local.Minute())
}
func priceLabel(price pgtype.Int4) string {
	if !price.Valid {
		return "Цена уточняется"
	}
	if price.Int32 == 0 {
		return "Бесплатно"
	}
	rubles, kopecks := price.Int32/100, price.Int32%100
	if kopecks == 0 {
		return fmt.Sprintf("от %s ₽", formatThousands(rubles))
	}
	return fmt.Sprintf("от %s,%02d ₽", formatThousands(rubles), kopecks)
}
func formatThousands(value int32) string {
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + " " + text[i:]
	}
	return text
}
