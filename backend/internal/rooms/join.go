package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

const joinRoute = "/api/v1/room-invites/{token}/join"

var errJoinMembershipChanged = errors.New("active membership changed while joining room")

func (s *Service) Join(ctx context.Context, principal contracts.Principal, token, key string) (api.RoomSnapshot, error) {
	var response api.RoomSnapshot
	if principal.UserID == uuid.Nil {
		return response, ErrUnauthenticated
	}
	if s == nil || s.pool == nil || s.recorder == nil || s.invites == nil {
		return response, ErrCreateUnavailable
	}
	tokenHash, requestHash, err := normalizeJoin(token, key)
	if err != nil {
		return response, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		err = s.WithTx(ctx, func(repo *Repository) error {
			if _, err := repo.Queries.LockMembershipUser(ctx, principal.UserID); err != nil {
				return err
			}
			idem := roomsql.GetCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: joinRoute}
			discoveredInvite, err := repo.Queries.GetRoomInviteByHash(ctx, tokenHash)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInviteNotFound
			}
			if err != nil {
				return err
			}

			discovered, membershipErr := repo.Queries.GetActiveMembership(ctx, principal.UserID)
			if membershipErr != nil && !errors.Is(membershipErr, pgx.ErrNoRows) {
				return membershipErr
			}
			roomIDs := []uuid.UUID{discoveredInvite.RoomID}
			if membershipErr == nil {
				roomIDs = append(roomIDs, discovered.RoomID)
			}
			lockedRooms, err := repo.LockRooms(ctx, roomIDs...)
			if err != nil {
				return err
			}
			roomsByID := make(map[uuid.UUID]roomsql.Room, len(lockedRooms))
			for _, room := range lockedRooms {
				roomsByID[room.ID] = room
			}
			target := roomsByID[discoveredInvite.RoomID]
			if target.State == string(RoomStateClosed) {
				return ErrRoomClosed
			}

			invite, err := repo.Queries.LockRoomInviteByHash(ctx, tokenHash)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInviteNotFound
			}
			if err != nil {
				return err
			}
			if invite.ID != discoveredInvite.ID || invite.RoomID != discoveredInvite.RoomID {
				return ErrInviteNotFound
			}

			active, activeErr := repo.Queries.LockActiveMembership(ctx, principal.UserID)
			if activeErr != nil && !errors.Is(activeErr, pgx.ErrNoRows) {
				return activeErr
			}
			if membershipErr == nil && (errors.Is(activeErr, pgx.ErrNoRows) || active.RoomID != discovered.RoomID) {
				return errJoinMembershipChanged
			}
			if errors.Is(membershipErr, pgx.ErrNoRows) && activeErr == nil {
				return errJoinMembershipChanged
			}
			// The locks above may block. Read the database clock only after they
			// are acquired, so expiration and idempotency use the time at which
			// this transaction can actually make its join decision.
			now, err := repo.Queries.ClockNow(ctx)
			if err != nil {
				return err
			}
			clock := now.Time
			cached, err := repo.Queries.GetCreateIdempotency(ctx, idem)
			if err == nil {
				if !cached.ExpiresAt.Time.After(clock) {
					if _, err := repo.Queries.DeleteExpiredCreateIdempotency(ctx, roomsql.DeleteExpiredCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: joinRoute, ExpiresAt: pgTimestamp(clock)}); err != nil {
						return err
					}
				} else {
					if cached.RequestHash != requestHash {
						return ErrIdempotencyConflict
					}
					plain, err := s.invites.OpenJoinResponse(principal.UserID, key, cached.ResponseBody)
					if err != nil {
						return err
					}
					return json.Unmarshal(plain, &response)
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}

			count, err := repo.Queries.CountRoomMembers(ctx, target.ID)
			if err != nil {
				return err
			}
			targetMembership, targetMembershipErr := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: target.ID, UserID: principal.UserID})
			if targetMembershipErr != nil && !errors.Is(targetMembershipErr, pgx.ErrNoRows) {
				return targetMembershipErr
			}
			// Membership is historical identity for a room. Matched and terminal
			// rooms retire active memberships, but a repeated Join must still be
			// idempotent and return the caller's current room snapshot.
			alreadyJoined := targetMembershipErr == nil
			switch roomInviteJoinability(invite.ExpiresAt.Time, target.ExpiresAt.Time, target.State, count, alreadyJoined, clock) {
			case api.Expired:
				return ErrInviteExpired
			case api.Full:
				return ErrRoomFull
			}
			if activeErr == nil && active.RoomID != target.ID {
				old := roomsByID[active.RoomID]
				if old.ExpiresAt.Time.After(clock) && (old.State == string(RoomStateCollectingIntents) || old.State == string(RoomStateRanking) || old.State == string(RoomStateVoting)) {
					return ErrActiveRoomExists
				}
				if _, err := repo.Queries.RetireRoomMemberships(ctx, old.ID); err != nil {
					return err
				}
				if _, err := repo.Queries.ClearRoomIntentCoordinates(ctx, old.ID); err != nil {
					return err
				}
				if _, err := repo.Queries.ExpireRoomInvites(ctx, roomsql.ExpireRoomInvitesParams{RoomID: old.ID, ExpiresAt: pgTimestamp(clock)}); err != nil {
					return err
				}
				if old.State == string(RoomStateExhausted) && old.RoundNo < 3 {
					if _, err := repo.Queries.ExpireRoomForReplacement(ctx, roomsql.ExpireRoomForReplacementParams{ID: old.ID, ExpiresAt: pgTimestamp(clock)}); err != nil {
						return err
					}
				} else if _, err := repo.Queries.BumpRoomVersion(ctx, old.ID); err != nil {
					return err
				}
			}
			if alreadyJoined {
				return s.finishJoinResponse(ctx, repo, principal.UserID, key, requestHash, target, targetMembership, clock, target.ExpiresAt.Time, &response)
			}

			targetMembership, err = repo.Queries.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: target.ID, UserID: principal.UserID, Role: "participant"})
			if err != nil {
				return mapJoinDBError(err)
			}
			if _, err := repo.Queries.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: target.ID, UserID: principal.UserID, RoundNo: target.RoundNo}); err != nil {
				return err
			}
			if _, err := repo.Queries.MarkRoomInviteConsumed(ctx, roomsql.MarkRoomInviteConsumedParams{ID: invite.ID, ConsumedBy: uuidToPG(principal.UserID), ConsumedAt: pgTimestamp(clock)}); err != nil {
				return err
			}
			if _, err := repo.Queries.BumpRoomVersion(ctx, target.ID); err != nil {
				return err
			}
			target, err = repo.Queries.GetRoom(ctx, target.ID)
			if err != nil {
				return err
			}
			if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: "room_joined", RoomID: &target.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: clock, DeduplicationKey: "room/" + target.ID.String() + "/joined/" + principal.UserID.String()}); err != nil {
				return err
			}
			return s.finishJoinResponse(ctx, repo, principal.UserID, key, requestHash, target, targetMembership, clock, invite.ExpiresAt.Time, &response)
		})
		if !errors.Is(err, errJoinMembershipChanged) {
			return response, err
		}
	}
	return api.RoomSnapshot{}, errJoinMembershipChanged
}

func (s *Service) finishJoinResponse(ctx context.Context, repo *Repository, userID uuid.UUID, key, requestHash string, room roomsql.Room, membership roomsql.RoomMember, now, expiresAt time.Time, response *api.RoomSnapshot) error {
	current, err := s.snapshot(ctx, repo, userID, room, membership, now)
	if err != nil {
		return err
	}
	*response = current
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	envelope, err := s.invites.SealJoinResponse(userID, key, body)
	if err != nil {
		return err
	}
	return repo.Queries.InsertCreateIdempotency(ctx, roomsql.InsertCreateIdempotencyParams{UserID: userID, Key: key, Route: joinRoute, RequestHash: requestHash, ResponseStatus: 200, ResponseBody: envelope, ExpiresAt: pgTimestamp(expiresAt)})
}

func uuidToPG(value uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: value, Valid: true} }

func normalizeJoin(token, key string) ([]byte, string, error) {
	if !utf8.ValidString(token) || utf8.RuneCountInString(token) < 16 || utf8.RuneCountInString(token) > 256 || !utf8.ValidString(key) || utf8.RuneCountInString(key) < 8 || utf8.RuneCountInString(key) > 128 {
		return nil, "", ErrValidation
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], hex.EncodeToString(sum[:]), nil
}

func mapJoinDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.ConstraintName == "user_one_active_room" {
			return ErrActiveRoomExists
		}
	}
	return err
}
