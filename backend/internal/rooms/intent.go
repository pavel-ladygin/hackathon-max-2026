package rooms

import (
	"context"
	"errors"
	"math"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

type normalizedIntent struct {
	dates                               []pgtype.Date
	days, slots, categories, exclusions []string
	budget                              int32
	lat, lng                            pgtype.Float8
	radius                              pgtype.Int4
	text                                pgtype.Text
}

func (s *Service) ReplaceIntent(ctx context.Context, principal contracts.Principal, roomID uuid.UUID, request api.RoomIntentRequest) (api.RoomSnapshot, bool, error) {
	var snapshot api.RoomSnapshot
	var transitioned bool
	if principal.UserID == uuid.Nil {
		return snapshot, false, ErrUnauthenticated
	}
	if s == nil || s.pool == nil || s.recorder == nil || s.invites == nil || s.builder == nil {
		return snapshot, false, ErrCreateUnavailable
	}
	base, err := normalizeIntent(request)
	if err != nil {
		return snapshot, false, err
	}
	err = s.withRoomBuildTx(ctx, func(repo *Repository) error {
		room, err := repo.Queries.LockRoom(ctx, roomID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
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
		nowValue, err := repo.Queries.ClockNow(ctx)
		if err != nil {
			return err
		}
		now := nowValue.Time
		if !room.ExpiresAt.Time.After(now) {
			return ErrRoomNotFound
		}
		switch RoomState(room.State) {
		case RoomStateMatched:
			return ErrAlreadyMatched
		case RoomStateExhausted:
			if room.RoundNo >= 3 {
				return ErrRoundLimitReached
			}
			return ErrIntentLocked
		case RoomStateRanking, RoomStateVoting:
			return ErrIntentLocked
		case RoomStateCollectingIntents:
			if !membership.IsActive {
				return ErrRoomNotFound
			}
		default:
			return ErrRoomNotFound
		}
		zoneName, err := repo.Queries.GetCityTimezone(ctx, room.CityID)
		if err != nil {
			return err
		}
		zone, err := time.LoadLocation(zoneName)
		if err != nil {
			return err
		}
		today := now.In(zone)
		today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, zone)
		for _, date := range base.dates {
			local := time.Date(date.Time.Year(), date.Time.Month(), date.Time.Day(), 0, 0, 0, 0, zone)
			if local.Before(today) {
				return ErrPastIntentDate
			}
		}
		stored, err := repo.Queries.UpsertRoomIntent(ctx, roomsql.UpsertRoomIntentParams{
			RoomID: room.ID, UserID: principal.UserID, RoundNo: room.RoundNo,
			DateOptions: base.dates, DayTypes: base.days, TimeSlots: base.slots,
			CategorySlugs: base.categories, BudgetMaxMinor: base.budget,
			LocationLat: base.lat, LocationLng: base.lng, RadiusM: base.radius,
			ExclusionSlugs: base.exclusions, FreeText: base.text,
		})
		if err != nil {
			return err
		}
		if _, err := repo.Queries.SetRoundReady(ctx, roomsql.SetRoundReadyParams{RoomID: room.ID, UserID: principal.UserID, RoundNo: room.RoundNo, Ready: true, IntentVersion: pgtype.Int4{Int32: stored.Version, Valid: true}}); err != nil {
			return err
		}
		both, err := repo.Queries.AreBothReady(ctx, roomsql.AreBothReadyParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		if both.Valid && both.Bool {
			if err := s.buildRoomPool(ctx, repo, room); err != nil {
				return err
			}
			transitioned = true
			room, err = repo.Queries.GetRoom(ctx, room.ID)
			if err != nil {
				return err
			}
		}
		if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: "intent_submit", RoomID: &room.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: now}); err != nil {
			return err
		}
		snapshot, err = s.snapshot(ctx, repo, principal.UserID, room, membership, now)
		return err
	})
	return snapshot, transitioned, err
}

func normalizeIntent(in api.RoomIntentRequest) (normalizedIntent, error) {
	var out normalizedIntent
	if len(in.Dates) < 1 || len(in.Dates) > 14 || in.DayTypes == nil || in.TimeSlots == nil || len(in.CategorySlugs) == 0 || in.ExclusionSlugs == nil || in.BudgetMaxMinor < 0 || in.BudgetMaxMinor > 100000000 {
		return out, ErrValidation
	}
	dateSeen := map[string]struct{}{}
	for _, date := range in.Dates {
		key := date.Time.Format(time.DateOnly)
		if date.Time.IsZero() || key != date.Time.Format("2006-01-02") || has(dateSeen, key) {
			return out, ErrValidation
		}
		dateSeen[key] = struct{}{}
		out.dates = append(out.dates, pgtype.Date{Time: date.Time, Valid: true})
	}
	var err error
	out.days, err = uniqueKnown(in.DayTypes, func(v api.DayType) bool { return v.Valid() })
	if err != nil {
		return out, err
	}
	out.slots, err = uniqueKnown(in.TimeSlots, func(v api.TimeSlot) bool { return v.Valid() })
	if err != nil {
		return out, err
	}
	out.categories, err = uniqueKnown(in.CategorySlugs, func(v api.CategorySlug) bool { return v.Valid() })
	if err != nil {
		return out, err
	}
	out.exclusions, err = uniqueKnown(in.ExclusionSlugs, func(v api.RoomIntentRequestExclusionSlugs) bool { return v.Valid() })
	if err != nil {
		return out, err
	}
	out.budget = int32(in.BudgetMaxMinor)
	if value, valueErr := in.Location.Get(); valueErr == nil {
		if math.IsNaN(float64(value.Lat)) || math.IsInf(float64(value.Lat), 0) || value.Lat < -90 || value.Lat > 90 || math.IsNaN(float64(value.Lng)) || math.IsInf(float64(value.Lng), 0) || value.Lng < -180 || value.Lng > 180 {
			return out, ErrValidation
		}
		out.lat, out.lng = pgtype.Float8{Float64: float64(value.Lat), Valid: true}, pgtype.Float8{Float64: float64(value.Lng), Valid: true}
	}
	if value, valueErr := in.RadiusM.Get(); valueErr == nil {
		if value < 100 || value > 50000 {
			return out, ErrValidation
		}
		out.radius = pgtype.Int4{Int32: int32(value), Valid: true}
	}
	if value, valueErr := in.FreeText.Get(); valueErr == nil {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 300 || bytesContainsNUL(value) {
			return out, ErrValidation
		}
		out.text = pgtype.Text{String: value, Valid: true}
	}
	return out, nil
}

func uniqueKnown[T ~string](values []T, valid func(T) bool) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key := string(value)
		if !valid(value) || has(seen, key) {
			return nil, ErrValidation
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out, nil
}

func has(set map[string]struct{}, value string) bool { _, ok := set[value]; return ok }
func bytesContainsNUL(value string) bool {
	for _, r := range value {
		if r == 0 {
			return true
		}
	}
	return false
}
