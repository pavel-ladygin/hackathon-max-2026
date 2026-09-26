package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
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

const (
	createRoute = "/api/v1/rooms"
	createTTL   = 48 * time.Hour
)

var errCreateMembershipChanged = errors.New("active membership changed while creating room")

// Create creates a collecting room and records its encrypted successful
// response for idempotent retries. All checks that need a stable view happen in
// one READ COMMITTED transaction after the user's serialization lock.
func (s *Service) Create(ctx context.Context, principal contracts.Principal, key string, request api.CreateRoomRequest) (api.CreateRoomResponse, error) {
	var response api.CreateRoomResponse
	if principal.UserID == uuid.Nil {
		return response, ErrUnauthenticated
	}
	if s == nil || s.pool == nil || s.recorder == nil || s.invites == nil {
		return response, ErrCreateUnavailable
	}
	name, hash, err := normalizeCreate(request, key)
	if err != nil {
		return response, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		err = s.WithTx(ctx, func(repo *Repository) error {
			if _, err := repo.Queries.LockMembershipUser(ctx, principal.UserID); err != nil {
				return err
			}
			now, err := repo.Queries.ClockNow(ctx)
			if err != nil {
				return err
			}
			clock := now.Time
			idem := roomsql.GetCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: createRoute}
			cached, err := repo.Queries.GetCreateIdempotency(ctx, idem)
			if err == nil {
				if !cached.ExpiresAt.Time.After(clock) {
					if _, err := repo.Queries.DeleteExpiredCreateIdempotency(ctx, roomsql.DeleteExpiredCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: createRoute, ExpiresAt: pgTimestamp(clock)}); err != nil {
						return err
					}
				} else {
					if cached.RequestHash != hash {
						return ErrIdempotencyConflict
					}
					plain, err := s.invites.OpenResponse(principal.UserID, key, cached.ResponseBody)
					if err != nil {
						return err
					}
					if err := json.Unmarshal(plain, &response); err != nil {
						return err
					}
					return nil
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}

			// The city lookup is deliberately after idempotency replay handling.
			exists, err := repo.Queries.CityExists(ctx, request.CityId)
			if err != nil {
				return err
			}
			if !exists {
				return ErrValidation
			}

			discovered, err := repo.Queries.GetActiveMembership(ctx, principal.UserID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			var lockedOld roomsql.Room
			if err == nil {
				locked, err := repo.LockRooms(ctx, discovered.RoomID)
				if err != nil {
					return err
				}
				lockedOld = locked[0]
			}
			membership, err := repo.Queries.LockActiveMembership(ctx, principal.UserID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && membership.RoomID != discovered.RoomID {
				return errCreateMembershipChanged
			}
			hasActiveMembership := err == nil
			// A peer may have retired both memberships while this request waited
			// for the old room. Refresh time even when the re-read found no row.
			now, err = repo.Queries.ClockNow(ctx)
			if err != nil {
				return err
			}
			clock = now.Time
			if hasActiveMembership {
				old := lockedOld
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

			expires := clock.Add(createTTL)
			room, err := repo.Queries.InsertRoom(ctx, roomsql.InsertRoomParams{ID: uuid.New(), CreatorUserID: principal.UserID, CityID: request.CityId, Name: name, State: string(RoomStateCollectingIntents), RoundNo: 1, ActivePoolVersion: 0, ExpiresAt: pgTimestamp(expires)})
			if err != nil {
				return mapCreateDBError(err)
			}
			if _, err := repo.Queries.InsertRoomMember(ctx, roomsql.InsertRoomMemberParams{RoomID: room.ID, UserID: principal.UserID, Role: "creator"}); err != nil {
				return mapCreateDBError(err)
			}
			if _, err := repo.Queries.InsertRoomRoundState(ctx, roomsql.InsertRoomRoundStateParams{RoomID: room.ID, UserID: principal.UserID, RoundNo: 1}); err != nil {
				return err
			}
			material, err := s.invites.New(room.ID, expires)
			if err != nil {
				return err
			}
			if _, err := repo.Queries.InsertRoomInvite(ctx, roomsql.InsertRoomInviteParams{ID: uuid.New(), RoomID: room.ID, TokenHash: material.Hash, TokenCiphertext: material.Ciphertext, EncryptionKeyVersion: material.KeyVersion, CreatedBy: principal.UserID, ExpiresAt: pgTimestamp(expires)}); err != nil {
				return err
			}
			participants, err := repo.Queries.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: room.ID, RoundNo: 1})
			if err != nil {
				return err
			}
			response = api.CreateRoomResponse{Room: createSnapshot(room, participants, material)}
			response.Invite.Token, response.Invite.Url, response.Invite.MaxDeepLink, response.Invite.ExpiresAt = material.Token, material.URL, material.MaxDeepLink, material.ExpiresAt
			if err := s.recorder.Record(ctx, repo.DBTX(), contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: principal.UserID, Type: "room_created", RoomID: &room.ID, RequestID: httpapi.RequestID(ctx), OccurredAt: clock, DeduplicationKey: "room/" + room.ID.String() + "/created"}); err != nil {
				return err
			}
			body, err := json.Marshal(response)
			if err != nil {
				return err
			}
			envelope, err := s.invites.SealResponse(principal.UserID, key, body)
			if err != nil {
				return err
			}
			return repo.Queries.InsertCreateIdempotency(ctx, roomsql.InsertCreateIdempotencyParams{UserID: principal.UserID, Key: key, Route: createRoute, RequestHash: hash, ResponseStatus: 201, ResponseBody: envelope, ExpiresAt: pgTimestamp(expires)})
		})
		if !errors.Is(err, errCreateMembershipChanged) {
			if err != nil {
				return api.CreateRoomResponse{}, err
			}
			return response, nil
		}
	}
	return api.CreateRoomResponse{}, errCreateMembershipChanged
}

func normalizeCreate(request api.CreateRoomRequest, key string) (string, string, error) {
	name := strings.TrimSpace(request.Name)
	if !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 || !utf8.ValidString(key) || strings.ContainsRune(key, '\x00') || utf8.RuneCountInString(key) < 8 || utf8.RuneCountInString(key) > 128 || request.CityId == uuid.Nil {
		return "", "", ErrValidation
	}
	payload, err := json.Marshal(struct {
		Name   string    `json:"name"`
		CityID uuid.UUID `json:"city_id"`
	}{Name: name, CityID: request.CityId})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(payload)
	return name, hex.EncodeToString(sum[:]), nil
}

func pgTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func mapCreateDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "user_one_active_room" {
		return ErrActiveRoomExists
	}
	return err
}
