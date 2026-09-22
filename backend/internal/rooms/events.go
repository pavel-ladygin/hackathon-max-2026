package rooms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

const (
	defaultRoomEventsLimit = 20
	maxRoomEventsLimit     = 50
)

type RoomEventsInput struct {
	Limit  int
	Cursor string
}

type poolExhaustedState struct {
	MyPoolFinished bool
	RoomExhausted  bool
}

func (s *Service) GetEvents(ctx context.Context, principal contracts.Principal, roomID uuid.UUID, input RoomEventsInput) (api.RoomEventsResponse, error) {
	var response api.RoomEventsResponse
	if principal.UserID == uuid.Nil || roomID == uuid.Nil || input.Limit < 0 || input.Limit > maxRoomEventsLimit || s.availability == nil || s.eventsCursor == nil {
		return response, ErrValidation
	}
	if input.Limit == 0 {
		input.Limit = defaultRoomEventsLimit
	}
	var committedError error
	err := s.WithTx(ctx, func(repo *Repository) error {
		// Serialize exhaustion with votes and with the other participant's
		// exhaustion check.  This keeps the per-member marker and the room
		// transition in one transaction.
		room, err := repo.Queries.LockRoom(ctx, roomID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		now, err := repo.Queries.ClockNow(ctx)
		if err != nil {
			return err
		}
		membership, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: roomID, UserID: principal.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		// Final-round cleanup retires memberships. Historical members may retry
		// their accepted events request and receive the terminal result, while a
		// user who never belonged to the room remains a 404 above.
		if room.State == string(RoomStateExhausted) {
			return poolExhaustedState{MyPoolFinished: true, RoomExhausted: true}
		}
		if !membership.IsActive || !room.ExpiresAt.Time.After(now.Time) {
			return ErrRoomNotFound
		}

		states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		finished := poolExhaustedState{}
		for _, state := range states {
			member, memberErr := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: room.ID, UserID: state.UserID})
			if memberErr != nil {
				return memberErr
			}
			if !member.IsActive {
				continue
			}
			if state.UserID == principal.UserID {
				finished.MyPoolFinished = state.PoolFinished
			}
		}
		bothFinished, err := repo.Queries.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		finished.RoomExhausted = bothFinished.Valid && bothFinished.Bool || room.State == string(RoomStateExhausted)
		if finished.MyPoolFinished || room.State == string(RoomStateExhausted) {
			return finished
		}
		if room.State != string(RoomStateVoting) {
			return ErrPoolNotReady
		}
		pool, err := repo.Queries.LockActivePool(ctx, room.ID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && pool.State != "ready" {
			return ErrPoolNotReady
		}
		if err != nil {
			return err
		}
		startPosition := int32(-1)
		if input.Cursor != "" {
			cursor, err := s.eventsCursor.Decode(input.Cursor, roomEventsCursor{RoomID: room.ID, PoolVersion: pool.Version, UserID: principal.UserID})
			if err != nil {
				return ErrInvalidRoomEventsCursor
			}
			startPosition = cursor.Position
		}

		budget, err := roomHardBudget(ctx, repo, room, states)
		if err != nil {
			return err
		}
		rows, err := repo.Queries.GetRoomEventCards(ctx, roomsql.GetRoomEventCardsParams{UserID: principal.UserID, PoolID: pool.ID})
		if err != nil {
			return err
		}
		eligible := make([]roomsql.GetRoomEventCardsRow, 0, len(rows))
		for _, row := range rows {
			if row.Voted {
				continue
			}
			availability, err := s.availability.CheckForRoomVote(ctx, row.EventID)
			if err != nil {
				return err
			}
			if !roomEventAvailable(availability, budget, now.Time) {
				continue
			}
			eligible = append(eligible, row)
		}
		response = api.RoomEventsResponse{RoomId: room.ID, PoolVersion: int(pool.Version), RoundNo: int(pool.RoundNo), Total: len(eligible), NextCursor: nullable.NewNullNullable[string](), Items: []struct {
			Cursor   string        `json:"cursor"`
			Event    api.EventCard `json:"event"`
			Position int           `json:"position"`
		}{}}
		remaining := make([]roomsql.GetRoomEventCardsRow, 0, len(eligible))
		for _, row := range eligible {
			if row.Position > startPosition {
				remaining = append(remaining, row)
			}
		}
		if len(eligible) == 0 {
			// Only a globally empty eligible set means that this member has
			// exhausted the pool.  A cursor positioned after the last item is
			// merely an empty page and must not persist completion.
			finish := api.VoteResponse{}
			if err := s.updatePoolFinished(ctx, repo, room, pool, principal.UserID, budget, &finish); err != nil {
				return err
			}
			// Availability is deliberately checked again by updatePoolFinished.
			// If it changed between reads, do not return or persist a false
			// exhausted result.
			if !finish.MyPoolFinished {
				return nil
			}
			committedError = poolExhaustedState{MyPoolFinished: true, RoomExhausted: finish.RoomExhausted}
			return nil
		}
		if len(remaining) == 0 {
			return nil
		}
		page := remaining
		if len(page) > input.Limit {
			page = page[:input.Limit]
		}
		for _, row := range page {
			cursor, err := s.eventsCursor.Encode(roomEventsCursor{RoomID: room.ID, PoolVersion: pool.Version, UserID: principal.UserID, Position: row.Position})
			if err != nil {
				return err
			}
			response.Items = append(response.Items, struct {
				Cursor   string        `json:"cursor"`
				Event    api.EventCard `json:"event"`
				Position int           `json:"position"`
			}{Cursor: cursor, Event: roomEventCard(row), Position: int(row.Position)})
		}
		if len(remaining) > len(page) {
			next, err := s.eventsCursor.Encode(roomEventsCursor{RoomID: room.ID, PoolVersion: pool.Version, UserID: principal.UserID, Position: page[len(page)-1].Position})
			if err != nil {
				return err
			}
			response.NextCursor = nullable.NewNullableWithValue(next)
		}
		return nil
	})
	if err != nil {
		return response, err
	}
	return response, committedError
}

func (s poolExhaustedState) Error() string { return ErrPoolExhausted.Error() }
func (s poolExhaustedState) Unwrap() error { return ErrPoolExhausted }

func roomHardBudget(ctx context.Context, repo *Repository, room roomsql.Room, states []roomsql.RoomMemberRoundState) (int32, error) {
	var budget int32 = -1
	for _, state := range states {
		member, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: room.ID, UserID: state.UserID})
		if err != nil {
			return 0, err
		}
		if !member.IsActive {
			continue
		}
		intent, err := repo.Queries.GetRoomIntent(ctx, roomsql.GetRoomIntentParams{RoomID: room.ID, UserID: state.UserID, RoundNo: room.RoundNo})
		if err != nil {
			return 0, err
		}
		if budget < 0 || intent.BudgetMaxMinor < budget {
			budget = intent.BudgetMaxMinor
		}
	}
	return budget, nil
}

func roomEventAvailable(a contracts.Availability, hardBudget int32, now time.Time) bool {
	return a.Exists && a.Status == "published" && a.StartsAt.After(now) && a.TicketAvailable && a.TicketURL != nil && *a.TicketURL != "" && a.PriceFromMinor != nil && hardBudget >= 0 && *a.PriceFromMinor <= hardBudget
}

func roomEventCard(row roomsql.GetRoomEventCardsRow) api.EventCard {
	card := api.EventCard{Id: row.EventID, Title: row.Title, CategorySlug: api.CategorySlug(row.CategorySlug), StartsAt: row.StartsAt.Time, Timezone: row.Timezone, DateLabel: roomDateLabel(row.StartsAt.Time, row.Timezone), VenueName: row.VenueName, Currency: api.EventCardCurrency(row.Currency), PriceLabel: roomPriceLabel(row.PriceFromMinor), Saved: row.Saved, Reasons: roomRecommendationReasons(row.Explanation), Subtitle: nullable.NewNullNullable[string](), ImageUrl: nullable.NewNullNullable[string](), PriceFromMinor: nullable.NewNullNullable[int](), DistanceM: nullable.NewNullNullable[int](), DistanceLabel: nullable.NewNullNullable[string]()}
	if row.Subtitle.Valid {
		card.Subtitle = nullable.NewNullableWithValue(row.Subtitle.String)
	}
	if row.ImageUrl != "" {
		card.ImageUrl = nullable.NewNullableWithValue(row.ImageUrl)
	}
	if row.PriceFromMinor.Valid {
		card.PriceFromMinor = nullable.NewNullableWithValue(int(row.PriceFromMinor.Int32))
	}
	return card
}

func roomRecommendationReasons(raw []byte) []api.RecommendationReason {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var explanations []contracts.Explanation
	if decoder.Decode(&explanations) != nil {
		return []api.RecommendationReason{}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return []api.RecommendationReason{}
	}
	result := make([]api.RecommendationReason, 0, 3)
	for _, explanation := range explanations {
		code := api.RecommendationReasonCode(explanation.Code)
		if !code.Valid() || !utf8.ValidString(explanation.Text) || explanation.Text == "" {
			continue
		}
		result = append(result, api.RecommendationReason{Code: code, Text: explanation.Text})
		if len(result) == 3 {
			break
		}
	}
	return result
}

func roomDateLabel(startsAt time.Time, timezone string) string {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return startsAt.UTC().Format("02.01, 15:04")
	}
	local := startsAt.In(location)
	months := [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
	return fmt.Sprintf("%d %s, %02d:%02d", local.Day(), months[local.Month()-1], local.Hour(), local.Minute())
}

func roomPriceLabel(price pgtype.Int4) string {
	if !price.Valid {
		return "Цена уточняется"
	}
	if price.Int32 == 0 {
		return "Бесплатно"
	}
	rubles, kopecks := price.Int32/100, price.Int32%100
	text := fmt.Sprintf("%d", rubles)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + " " + text[i:]
	}
	if kopecks == 0 {
		return fmt.Sprintf("от %s ₽", text)
	}
	return fmt.Sprintf("от %s,%02d ₽", text, kopecks)
}
