package rooms

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// Get returns a caller-scoped room view from one consistent, read-only snapshot.
func (s *Service) Get(ctx context.Context, principal contracts.Principal, roomID uuid.UUID) (api.RoomSnapshot, error) {
	var snapshot api.RoomSnapshot
	if principal.UserID == uuid.Nil {
		return snapshot, ErrUnauthenticated
	}
	if s == nil || s.pool == nil || s.invites == nil {
		return snapshot, ErrCreateUnavailable
	}
	err := s.WithReadTx(ctx, func(repo *Repository) error {
		room, err := repo.Queries.GetRoom(ctx, roomID)
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
		now, err := repo.Queries.ClockNow(ctx)
		if err != nil {
			return err
		}
		if !room.ExpiresAt.Time.After(now.Time) {
			return ErrRoomNotFound
		}

		participants, err := repo.Queries.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		snapshot = baseRoomSnapshot(room, participants)

		intent, err := repo.Queries.GetRoomIntent(ctx, roomsql.GetRoomIntentParams{RoomID: room.ID, UserID: principal.UserID, RoundNo: room.RoundNo})
		if err == nil {
			snapshot.MyIntent = nullable.NewNullableWithValue(apiIntent(intent))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var myPoolFinished, myIntentReady bool
		states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		for _, state := range states {
			if state.UserID == principal.UserID {
				myPoolFinished = state.PoolFinished
				myIntentReady = state.Ready
				break
			}
		}

		pool, err := repo.Queries.GetActivePool(ctx, room.ID)
		if err == nil {
			voted, err := repo.Queries.CountPoolVotesByUser(ctx, roomsql.CountPoolVotesByUserParams{PoolID: pool.ID, UserID: principal.UserID})
			if err != nil {
				return err
			}
			bothFinished, err := repo.Queries.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo})
			if err != nil {
				return err
			}
			snapshot.Pool = nullable.NewNullableWithValue(poolSummary(pool, int(voted), myPoolFinished, room.State == string(api.RoomStateExhausted) || (bothFinished.Valid && bothFinished.Bool)))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		match, err := repo.Queries.GetRoomMatch(ctx, room.ID)
		if err == nil {
			snapshot.Match = nullable.NewNullableWithValue(api.MatchSummary{
				Id: match.ID, RoomId: match.RoomID, EventId: match.EventID,
				MatchedAt: match.MatchedAt.Time, Participants: snapshot.Participants,
			})
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		inviteAvailable := false
		if membership.Role == "creator" {
			invite, err := repo.Queries.GetRoomInviteForCreator(ctx, roomsql.GetRoomInviteForCreatorParams{RoomID: room.ID, CreatedBy: principal.UserID})
			if err == nil {
				material, err := s.invites.Recover(room.ID, invite.TokenCiphertext, invite.EncryptionKeyVersion, invite.ExpiresAt.Time)
				if err != nil {
					return err
				}
				snapshot.Invite = nullable.NewNullableWithValue(api.RoomInvite{Url: material.URL, MaxDeepLink: material.MaxDeepLink, ExpiresAt: material.ExpiresAt})
				inviteAvailable = invite.ExpiresAt.Time.After(now.Time) && len(participants) < 2
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		snapshot.AllowedActions = roomActions(room, myPoolFinished, myIntentReady, inviteAvailable)
		return nil
	})
	return snapshot, err
}

func baseRoomSnapshot(room roomsql.Room, participants []roomsql.GetPublicParticipantsRow) api.RoomSnapshot {
	snapshot := createSnapshot(room, participants, InviteMaterial{})
	snapshot.Invite = nullable.NewNullNullable[api.RoomInvite]()
	snapshot.AllowedActions = []api.RoomSnapshotAllowedActions{}
	return snapshot
}

func apiIntent(intent roomsql.RoomIntent) api.MyIntent {
	dates := make([]openapi_types.Date, 0, len(intent.DateOptions))
	for _, date := range intent.DateOptions {
		dates = append(dates, openapi_types.Date{Time: date.Time})
	}
	days := make([]api.DayType, len(intent.DayTypes))
	for i, value := range intent.DayTypes {
		days[i] = api.DayType(value)
	}
	slots := make([]api.TimeSlot, len(intent.TimeSlots))
	for i, value := range intent.TimeSlots {
		slots[i] = api.TimeSlot(value)
	}
	categories := make([]api.CategorySlug, len(intent.CategorySlugs))
	for i, value := range intent.CategorySlugs {
		categories[i] = api.CategorySlug(value)
	}
	exclusions := make([]api.MyIntentExclusionSlugs, len(intent.ExclusionSlugs))
	for i, value := range intent.ExclusionSlugs {
		exclusions[i] = api.MyIntentExclusionSlugs(value)
	}
	result := api.MyIntent{
		RoundNo: int(intent.RoundNo), Version: int(intent.Version), SubmittedAt: intent.SubmittedAt.Time,
		Dates: dates, DayTypes: days, TimeSlots: slots, CategorySlugs: categories,
		BudgetMaxMinor: int(intent.BudgetMaxMinor), ExclusionSlugs: exclusions,
		Location: nullable.NewNullNullable[api.GeoPoint](), RadiusM: nullable.NewNullNullable[int](), FreeText: nullable.NewNullNullable[string](),
	}
	if intent.LocationLat.Valid && intent.LocationLng.Valid {
		result.Location = nullable.NewNullableWithValue(api.GeoPoint{Lat: float32(intent.LocationLat.Float64), Lng: float32(intent.LocationLng.Float64)})
	}
	if intent.RadiusM.Valid {
		result.RadiusM = nullable.NewNullableWithValue(int(intent.RadiusM.Int32))
	}
	if intent.FreeText.Valid {
		result.FreeText = nullable.NewNullableWithValue(intent.FreeText.String)
	}
	return result
}

func roomActions(room roomsql.Room, myPoolFinished, myIntentReady, inviteAvailable bool) []api.RoomSnapshotAllowedActions {
	actions := make([]api.RoomSnapshotAllowedActions, 0, 3)
	switch api.RoomState(room.State) {
	case api.RoomStateCollectingIntents:
		if !myIntentReady {
			actions = append(actions, api.EditIntent)
		} else {
			actions = append(actions, api.Wait)
		}
		if inviteAvailable {
			actions = append(actions, api.Invite)
		}
	case api.RoomStateRanking:
		actions = append(actions, api.Wait)
		if inviteAvailable {
			actions = append(actions, api.Invite)
		}
	case api.RoomStateVoting:
		actions = append(actions, api.ViewPool)
		if myPoolFinished {
			actions = append(actions, api.Wait)
		} else {
			actions = append(actions, api.Vote)
		}
	case api.RoomStateMatched:
		actions = append(actions, api.ViewMatch)
	case api.RoomStateExhausted:
		if room.RoundNo < 3 {
			actions = append(actions, api.RestartWithNewIntent)
		}
	}
	return actions
}
