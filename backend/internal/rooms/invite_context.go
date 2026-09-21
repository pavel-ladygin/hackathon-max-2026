package rooms

import (
	"context"
	"crypto/sha256"
	"errors"

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
		status := api.Joinable
		if !preview.ExpiresAt.Time.After(now.Time) || !preview.RoomExpiresAt.Time.After(now.Time) {
			status = api.Expired
		} else if preview.MemberCount >= 2 && !preview.AlreadyJoined {
			status = api.Full
		}
		inviter := api.PublicParticipant{
			Id: preview.InviterID, DisplayName: preview.InviterDisplayName,
			Role: api.PublicParticipantRole("creator"), IntentReady: preview.InviterIntentReady,
			AvatarUrl: nullable.NewNullNullable[string](),
		}
		if preview.InviterAvatarUrl.Valid {
			inviter.AvatarUrl = nullable.NewNullableWithValue(preview.InviterAvatarUrl.String)
		}
		result = &api.InviteContext{Token: token, RoomName: preview.RoomName, Inviter: inviter, ExpiresAt: preview.ExpiresAt.Time, AlreadyJoined: preview.AlreadyJoined, Status: status}
		return nil
	})
	return result, err
}
