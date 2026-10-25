package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
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

const intentRoute = "/api/v1/rooms/{roomId}/intent/me"

type normalizedIntent struct {
	dates                               []pgtype.Date
	days, slots, categories, exclusions []string
	budget                              int32
	lat, lng                            pgtype.Float8
	radius                              pgtype.Int4
	text                                pgtype.Text
}

func (s *Service) ReplaceIntent(ctx context.Context, principal contracts.Principal, roomID uuid.UUID, request api.RoomIntentRequest, key string) (api.RoomSnapshot, bool, error) {
	var snapshot api.RoomSnapshot
	var transitioned bool
	if principal.UserID == uuid.Nil {
		return snapshot, false, ErrUnauthenticated
	}
	if s == nil || s.pool == nil || s.recorder == nil || s.invites == nil || s.builder == nil {
		return snapshot, false, ErrCreateUnavailable
	}
	if !utf8.ValidString(key) || utf8.RuneCountInString(key) < 8 || utf8.RuneCountInString(key) > 128 {
		return snapshot, false, ErrValidation
	}
	base, err := normalizeIntent(request)
	if err != nil {
		return snapshot, false, err
	}
	hash, err := intentRequestHash(roomID, request)
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
		idem := roomsql.GetCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: intentRoute}
		cached, err := repo.Queries.GetCreateIdempotency(ctx, idem)
		if err == nil {
			if !cached.ExpiresAt.Time.After(now) {
				if _, err := repo.Queries.DeleteExpiredCreateIdempotency(ctx, roomsql.DeleteExpiredCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: intentRoute, ExpiresAt: pgTimestamp(now)}); err != nil {
					return err
				}
			} else {
				if cached.RequestHash != hash {
					return ErrIdempotencyConflict
				}
				if !room.ExpiresAt.Time.After(now) {
					return ErrRoomNotFound
				}
				snapshot, err = s.snapshot(ctx, repo, principal.UserID, room, membership, now)
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if request.ExpectedRoundNo != int(room.RoundNo) || request.ExpectedRoomVersion != int(room.Version) {
			return ErrIntentConflict
		}
		if !room.ExpiresAt.Time.After(now) {
			return ErrRoomNotFound
		}
		if RoomState(room.State) == RoomStateClosed {
			return ErrRoomClosed
		}
		if RoomState(room.State) == RoomStateMatched {
			return ErrAlreadyMatched
		}
		// Final-round exhaustion retires memberships, but a known participant
		// must still receive the stable terminal outcome on a restart attempt.
		if RoomState(room.State) == RoomStateExhausted && room.RoundNo >= 3 {
			return ErrRoundLimitReached
		}
		if !membership.IsActive {
			return ErrRoomNotFound
		}
		targetRound := room.RoundNo
		switch RoomState(room.State) {
		case RoomStateExhausted:
			if err := repo.Queries.CopyRoomIntentsToNextRound(ctx, roomsql.CopyRoomIntentsToNextRoundParams{
				RoomID: room.ID, RoundNo: room.RoundNo, RoundNo_2: room.RoundNo + 1,
			}); err != nil {
				return err
			}
			targetRound++
			if err := repo.Queries.InsertNextRoundStates(ctx, roomsql.InsertNextRoundStatesParams{RoomID: room.ID, RoundNo: targetRound}); err != nil {
				return err
			}
			nextStates, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: targetRound})
			if err != nil {
				return err
			}
			for _, state := range nextStates {
				if state.UserID == principal.UserID && state.Ready {
					return ErrIntentConflict
				}
			}
		case RoomStateRanking, RoomStateVoting:
			return ErrIntentLocked
		case RoomStateCollectingIntents:
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
			RoomID: room.ID, UserID: principal.UserID, RoundNo: targetRound,
			DateOptions: base.dates, DayTypes: base.days, TimeSlots: base.slots,
			CategorySlugs: base.categories, BudgetMaxMinor: base.budget,
			LocationLat: base.lat, LocationLng: base.lng, RadiusM: base.radius,
			ExclusionSlugs: base.exclusions, FreeText: base.text,
		})
		if err != nil {
			return err
		}
		if _, err := repo.Queries.SetRoundReady(ctx, roomsql.SetRoundReadyParams{RoomID: room.ID, UserID: principal.UserID, RoundNo: targetRound, Ready: true, IntentVersion: pgtype.Int4{Int32: stored.Version, Valid: true}}); err != nil {
			return err
		}
		both, err := repo.Queries.AreBothReady(ctx, roomsql.AreBothReadyParams{RoomID: room.ID, RoundNo: targetRound})
		if err != nil {
			return err
		}
		if RoomState(room.State) == RoomStateExhausted && !(both.Valid && both.Bool) {
			if _, err := repo.Queries.BumpRoomVersion(ctx, room.ID); err != nil {
				return err
			}
			room, err = repo.Queries.GetRoom(ctx, room.ID)
			if err != nil {
				return err
			}
		} else if both.Valid && both.Bool {
			if RoomState(room.State) == RoomStateExhausted {
				room, err = repo.Queries.RestartExhaustedRoomRound(ctx, room.ID)
				if err != nil {
					return err
				}
			}
			if err := s.buildRoomPool(ctx, repo, room, principal.UserID, now); err != nil {
				return err
			}
			transitioned = true
			room, err = repo.Queries.GetRoom(ctx, room.ID)
			if err != nil {
				return err
			}
		}
		if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: "room_intent_submitted", RoomID: &room.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: now}); err != nil {
			return err
		}
		snapshot, err = s.snapshot(ctx, repo, principal.UserID, room, membership, now)
		if err != nil {
			return err
		}
		return repo.Queries.InsertCreateIdempotency(ctx, roomsql.InsertCreateIdempotencyParams{
			UserID: principal.UserID, Key: key, Route: intentRoute, RequestHash: hash,
			ResponseStatus: 200, ResponseBody: []byte(`{}`), ExpiresAt: room.ExpiresAt,
		})
	})
	return snapshot, transitioned, err
}

func normalizeIntent(in api.RoomIntentRequest) (normalizedIntent, error) {
	var out normalizedIntent
	if in.ExpectedRoundNo < 1 || in.ExpectedRoundNo > 3 || in.ExpectedRoomVersion < 1 || len(in.Dates) < 1 || len(in.Dates) > 14 || in.DayTypes == nil || in.TimeSlots == nil || len(in.CategorySlugs) == 0 || in.ExclusionSlugs == nil || in.BudgetMaxMinor < 0 || in.BudgetMaxMinor > 100000000 {
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

func intentRequestHash(roomID uuid.UUID, request api.RoomIntentRequest) (string, error) {
	dates := make([]string, 0, len(request.Dates))
	for _, date := range request.Dates {
		dates = append(dates, date.Time.Format(time.DateOnly))
	}
	sort.Strings(dates)
	dayTypes := canonicalIntentStrings(request.DayTypes)
	timeSlots := canonicalIntentStrings(request.TimeSlots)
	categories := canonicalIntentStrings(request.CategorySlugs)
	exclusions := canonicalIntentStrings(request.ExclusionSlugs)
	var location *[2]float32
	if value, err := request.Location.Get(); err == nil {
		location = &[2]float32{value.Lat, value.Lng}
	}
	var radius *int
	if value, err := request.RadiusM.Get(); err == nil {
		radius = &value
	}
	var freeText *string
	if value, err := request.FreeText.Get(); err == nil {
		freeText = &value
	}
	payload, err := json.Marshal(struct {
		RoomID              uuid.UUID   `json:"room_id"`
		ExpectedRoundNo     int         `json:"expected_round_no"`
		ExpectedRoomVersion int         `json:"expected_room_version"`
		Dates               []string    `json:"dates"`
		DayTypes            []string    `json:"day_types"`
		TimeSlots           []string    `json:"time_slots"`
		CategorySlugs       []string    `json:"category_slugs"`
		BudgetMaxMinor      int         `json:"budget_max_minor"`
		Location            *[2]float32 `json:"location"`
		RadiusM             *int        `json:"radius_m"`
		ExclusionSlugs      []string    `json:"exclusion_slugs"`
		FreeText            *string     `json:"free_text"`
	}{
		RoomID: roomID, ExpectedRoundNo: request.ExpectedRoundNo, ExpectedRoomVersion: request.ExpectedRoomVersion,
		Dates: dates, DayTypes: dayTypes, TimeSlots: timeSlots, CategorySlugs: categories,
		BudgetMaxMinor: request.BudgetMaxMinor, Location: location, RadiusM: radius,
		ExclusionSlugs: exclusions, FreeText: freeText,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalIntentStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	sort.Strings(result)
	return result
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
