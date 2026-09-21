package rooms

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

type stalePoolVersionError struct{ Current int }

func (e stalePoolVersionError) Error() string { return ErrStalePoolVersion.Error() }
func (e stalePoolVersionError) Unwrap() error { return ErrStalePoolVersion }

func (s *Service) Vote(ctx context.Context, principal contracts.Principal, roomID, eventID uuid.UUID, request api.VoteRequest) (api.VoteResponse, error) {
	response := api.VoteResponse{AcceptedVote: request.Vote, PoolVersion: request.PoolVersion, Match: nullable.NewNullNullable[api.Match]()}
	var committedError error
	if principal.UserID == uuid.Nil {
		return response, ErrUnauthenticated
	}
	if roomID == uuid.Nil || eventID == uuid.Nil || request.PoolVersion < 1 || !request.Vote.Valid() || s == nil || s.pool == nil || s.availability == nil || s.recorder == nil {
		return response, ErrValidation
	}
	err := s.WithTx(ctx, func(repo *Repository) error {
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
		membership, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: room.ID, UserID: principal.UserID})
		// Membership is intentionally checked before the terminal-state branch:
		// historical members must still receive ALREADY_MATCHED after their
		// memberships are retired, while users who never joined must remain
		// indistinguishable from a missing room.
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		if room.State == string(RoomStateMatched) {
			return ErrAlreadyMatched
		}
		if !membership.IsActive || !room.ExpiresAt.Time.After(now.Time) {
			return ErrRoomNotFound
		}
		if room.State == string(RoomStateExhausted) {
			return ErrPoolExhausted
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
		response.PoolVersion = int(pool.Version)
		if int(pool.Version) != request.PoolVersion {
			return stalePoolVersionError{Current: int(pool.Version)}
		}
		if _, err := repo.Queries.GetRoomPoolEvent(ctx, roomsql.GetRoomPoolEventParams{PoolID: pool.ID, EventID: eventID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		} else if err != nil {
			return err
		}
		states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		budget, err := roomHardBudget(ctx, repo, room, states)
		if err != nil {
			return err
		}
		existing, err := repo.Queries.GetRoomVote(ctx, roomsql.GetRoomVoteParams{PoolID: pool.ID, EventID: eventID, UserID: principal.UserID})
		if err == nil {
			if existing.Vote != string(request.Vote) {
				return ErrVoteAlreadyCast
			}
			if err := s.updatePoolFinished(ctx, repo, room, pool, principal.UserID, budget, &response); err != nil {
				return err
			}
			return s.currentVoteResponse(ctx, repo, room, pool, principal.UserID, &response)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		availability, err := s.availability.CheckForRoomVote(ctx, eventID)
		if err != nil {
			return err
		}
		if !roomEventAvailable(availability, budget) {
			if err := s.updatePoolFinished(ctx, repo, room, pool, principal.UserID, budget, &response); err != nil {
				return err
			}
			committedError = ErrEventUnavailable
			return nil
		}
		inserted, err := repo.Queries.InsertRoomVote(ctx, roomsql.InsertRoomVoteParams{PoolID: pool.ID, RoomID: room.ID, EventID: eventID, UserID: principal.UserID, Vote: string(request.Vote)})
		if err != nil {
			return err
		}
		if inserted != 1 {
			return ErrVoteAlreadyCast
		}
		if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: string(request.Vote), EventID: &eventID, RoomID: &room.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: now.Time}); err != nil {
			return err
		}
		if request.Vote == api.Like {
			likes, err := repo.Queries.CountPoolLikes(ctx, roomsql.CountPoolLikesParams{PoolID: pool.ID, EventID: eventID})
			if err != nil {
				return err
			}
			if likes == 2 {
				matchID := uuid.New()
				inserted, err := repo.Queries.InsertRoomMatch(ctx, roomsql.InsertRoomMatchParams{ID: matchID, RoomID: room.ID, PoolID: pool.ID, EventID: eventID})
				if err != nil {
					return err
				}
				if inserted != 1 {
					return ErrAlreadyMatched
				}
				if n, err := repo.Queries.MarkRoomMatched(ctx, roomsql.MarkRoomMatchedParams{ID: room.ID, EventID: pgtype.UUID{Bytes: eventID, Valid: true}}); err != nil || n != 1 {
					if err != nil {
						return err
					}
					return ErrAlreadyMatched
				}
				// Build the response while memberships are still active. The
				// terminal side effects below retire them as part of this same
				// transaction, so any response error still rolls everything back.
				if err := s.currentVoteResponse(ctx, repo, room, pool, principal.UserID, &response); err != nil {
					return err
				}
				if _, err := repo.Queries.RetireRoomMemberships(ctx, room.ID); err != nil {
					return err
				}
				if _, err := repo.Queries.ClearRoomIntentCoordinates(ctx, room.ID); err != nil {
					return err
				}
				if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: "match", EventID: &eventID, RoomID: &room.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: now.Time}); err != nil {
					return err
				}
				return nil
			}
		}
		return s.updatePoolFinished(ctx, repo, room, pool, principal.UserID, budget, &response)
	})
	if err != nil {
		return response, err
	}
	return response, committedError
}

func (s *Service) updatePoolFinished(ctx context.Context, repo *Repository, room roomsql.Room, pool roomsql.RoomPool, userID uuid.UUID, budget int32, response *api.VoteResponse) error {
	finished, err := s.memberFinishedPool(ctx, repo, room, pool, userID, budget)
	if err != nil {
		return err
	}
	if response != nil {
		response.MyPoolFinished = finished
	}
	if !finished {
		return nil
	}
	if _, err := repo.Queries.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{RoomID: room.ID, UserID: userID, RoundNo: room.RoundNo}); err != nil {
		return err
	}
	both, err := repo.Queries.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	if !both.Valid || !both.Bool {
		return nil
	}
	if n, err := repo.Queries.MarkRoomExhausted(ctx, room.ID); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return ErrPoolNotReady
	}
	if response != nil {
		response.RoomExhausted = true
	}
	if err := s.finalizeExhaustedRoom(ctx, repo, room); err != nil {
		return err
	}
	return nil
}

// finalizeExhaustedRoom applies terminal cleanup only to the final round.
// Earlier rounds remain restartable and retain their active memberships and
// intent coordinates.
func (s *Service) finalizeExhaustedRoom(ctx context.Context, repo *Repository, room roomsql.Room) error {
	if room.RoundNo != 3 {
		return nil
	}
	if _, err := repo.Queries.RetireRoomMemberships(ctx, room.ID); err != nil {
		return err
	}
	_, err := repo.Queries.ClearRoomIntentCoordinates(ctx, room.ID)
	return err
}

func (s *Service) memberFinishedPool(ctx context.Context, repo *Repository, room roomsql.Room, pool roomsql.RoomPool, userID uuid.UUID, budget int32) (bool, error) {
	cards, err := repo.Queries.GetRoomEventCards(ctx, roomsql.GetRoomEventCardsParams{UserID: userID, PoolID: pool.ID})
	if err != nil {
		return false, err
	}
	for _, card := range cards {
		if card.Voted {
			continue
		}
		a, err := s.availability.CheckForRoomVote(ctx, card.EventID)
		if err != nil {
			return false, err
		}
		if roomEventAvailable(a, budget) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) currentVoteResponse(ctx context.Context, repo *Repository, room roomsql.Room, pool roomsql.RoomPool, userID uuid.UUID, response *api.VoteResponse) error {
	match, err := repo.Queries.GetRoomMatch(ctx, room.ID)
	if err == nil {
		participants, err := repo.Queries.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: room.ID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		cards, err := repo.Queries.GetRoomEventCards(ctx, roomsql.GetRoomEventCardsParams{UserID: userID, PoolID: pool.ID})
		if err != nil {
			return err
		}
		for _, card := range cards {
			if card.EventID == match.EventID {
				public := createSnapshot(room, participants, InviteMaterial{}).Participants
				response.Match = nullable.NewNullableWithValue(api.Match{Id: match.ID, MatchedAt: match.MatchedAt.Time, Event: roomEventCard(card), Participants: public})
				return nil
			}
		}
		return ErrRoomNotFound
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.UserID == userID {
			response.MyPoolFinished = state.PoolFinished
			break
		}
	}
	both, err := repo.Queries.AreBothPoolFinished(ctx, roomsql.AreBothPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	response.RoomExhausted = both.Valid && both.Bool
	return nil
}
