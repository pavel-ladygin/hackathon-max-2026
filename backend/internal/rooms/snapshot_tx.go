package rooms

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// snapshot builds a caller-scoped response using only the supplied transaction.
func (s *Service) snapshot(ctx context.Context, repo *Repository, userID uuid.UUID, room roomsql.Room, membership roomsql.RoomMember, now time.Time) (api.RoomSnapshot, error) {
	participants, err := repo.Queries.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return api.RoomSnapshot{}, err
	}
	result := baseRoomSnapshot(room, participants)
	intent, err := repo.Queries.GetRoomIntent(ctx, roomsql.GetRoomIntentParams{RoomID: room.ID, UserID: userID, RoundNo: room.RoundNo})
	if err == nil {
		result.MyIntent = nullable.NewNullableWithValue(apiIntent(intent))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return result, err
	}
	myPoolFinished, myIntentReady := false, false
	for _, state := range states {
		if state.UserID == userID {
			myPoolFinished = state.PoolFinished
			myIntentReady = state.Ready
			break
		}
	}
	pool, err := repo.Queries.GetActivePool(ctx, room.ID)
	if err == nil {
		voted, err := repo.Queries.CountPoolVotesByUser(ctx, roomsql.CountPoolVotesByUserParams{PoolID: pool.ID, UserID: userID})
		if err != nil {
			return result, err
		}
		bothFinished, err := repo.Queries.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return result, err
		}
		result.Pool = nullable.NewNullableWithValue(poolSummary(pool, int(voted), myPoolFinished, room.State == string(api.RoomStateExhausted) || (bothFinished.Valid && bothFinished.Bool)))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	match, err := repo.Queries.GetRoomMatch(ctx, room.ID)
	if err == nil {
		result.Match = nullable.NewNullableWithValue(api.MatchSummary{Id: match.ID, RoomId: match.RoomID, EventId: match.EventID, MatchedAt: match.MatchedAt.Time, Participants: result.Participants})
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	inviteAvailable := false
	if membership.Role == "creator" {
		invite, err := repo.Queries.GetRoomInviteForCreator(ctx, roomsql.GetRoomInviteForCreatorParams{RoomID: room.ID, CreatedBy: userID})
		if err == nil {
			material, err := s.invites.Recover(room.ID, invite.TokenCiphertext, invite.EncryptionKeyVersion, invite.ExpiresAt.Time)
			if err != nil {
				return result, err
			}
			result.Invite = nullable.NewNullableWithValue(api.RoomInvite{Url: material.URL, MaxDeepLink: material.MaxDeepLink, ExpiresAt: material.ExpiresAt})
			inviteAvailable = invite.ExpiresAt.Time.After(now) && len(participants) < 2
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
	}
	result.AllowedActions = roomActions(room, myPoolFinished, myIntentReady, inviteAvailable)
	return result, nil
}
