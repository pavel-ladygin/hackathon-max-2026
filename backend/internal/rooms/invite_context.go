package rooms

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func (s *Service) ResolveInviteContext(ctx context.Context, userID uuid.UUID, token string) (*api.InviteContext, error) {
	if userID == uuid.Nil || token == "" {
		return nil, nil
	}
	var result *api.InviteContext
	hash := sha256.Sum256([]byte(token))
	err := s.WithReadTx(ctx, func(repo *Repository) error {
		preview, err := repo.Queries.GetInvitePreviewByHash(ctx, roomsql.GetInvitePreviewByHashParams{TokenHash: hash[:], UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		now, err := repo.Queries.ClockNow(ctx)
		if err != nil {
			return err
		}
		status := roomInviteJoinability(preview.ExpiresAt.Time, preview.RoomExpiresAt.Time, preview.RoomState, preview.MemberCount, preview.AlreadyJoined, now.Time)
		inviter := api.PublicParticipant{
			Id: preview.InviterID, DisplayName: preview.InviterDisplayName,
			Role: api.PublicParticipantRole("creator"), IntentReady: preview.InviterIntentReady,
			AvatarUrl: nullable.NewNullNullable[string](),
		}
		if preview.InviterAvatarUrl.Valid {
			inviter.AvatarUrl = nullable.NewNullableWithValue(preview.InviterAvatarUrl.String)
		}
		result = &api.InviteContext{Token: token, RoomId: preview.RoomID, RoomName: preview.RoomName, Inviter: inviter, ExpiresAt: preview.ExpiresAt.Time, AlreadyJoined: preview.AlreadyJoined, Status: status}
		return nil
	})
	return result, err
}

// roomInviteJoinability is shared by preview and Join. Keep this limited to
// the three public preview states so Join returns the same expiry/capacity
// outcome that a caller just observed in its preview.
func roomInviteJoinability(inviteExpiresAt, roomExpiresAt time.Time, roomState string, memberCount int64, alreadyJoined bool, now time.Time) api.InviteContextStatus {
	if !inviteExpiresAt.After(now) || !roomExpiresAt.After(now) {
		return api.Expired
	}
	if alreadyJoined {
		return api.Joinable
	}
	if roomState != string(RoomStateCollectingIntents) || memberCount >= 2 {
		return api.Full
	}
	return api.Joinable
}
