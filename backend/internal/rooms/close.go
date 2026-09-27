package rooms

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// Close ends a room for both participants and leaves historical membership rows
// intact so each member can still read the terminal snapshot.
func (s *Service) Close(ctx context.Context, principal contracts.Principal, roomID uuid.UUID) error {
	if principal.UserID == uuid.Nil {
		return ErrUnauthenticated
	}
	if s == nil || s.pool == nil {
		return ErrCreateUnavailable
	}
	return s.WithTx(ctx, func(repo *Repository) error {
		if _, err := repo.Queries.LockMembershipUser(ctx, principal.UserID); errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		} else if err != nil {
			return err
		}
		room, err := repo.Queries.LockRoom(ctx, roomID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		if _, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: roomID, UserID: principal.UserID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		} else if err != nil {
			return err
		}
		if room.State == string(RoomStateClosed) {
			return nil
		}
		if _, err := repo.Queries.CloseRoom(ctx, roomsql.CloseRoomParams{ID: roomID, ClosedBy: pgtype.UUID{Bytes: principal.UserID, Valid: true}}); err != nil {
			return err
		}
		members, err := repo.Queries.GetPublicParticipants(ctx, roomsql.GetPublicParticipantsParams{RoomID: roomID, RoundNo: room.RoundNo})
		if err != nil {
			return err
		}
		for _, member := range members {
			if member.UserID != principal.UserID {
				if err := repo.Queries.InsertRoomCloseNotice(ctx, roomsql.InsertRoomCloseNoticeParams{RoomID: roomID, RecipientUserID: member.UserID}); err != nil {
					return err
				}
			}
		}
		_, err = repo.Queries.RetireRoomMemberships(ctx, roomID)
		return err
	})
}

func (s *Service) AcknowledgeCloseNotice(ctx context.Context, principal contracts.Principal, roomID uuid.UUID) error {
	if principal.UserID == uuid.Nil {
		return ErrUnauthenticated
	}
	if s == nil || s.pool == nil {
		return ErrCreateUnavailable
	}
	return s.WithTx(ctx, func(repo *Repository) error {
		if _, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: roomID, UserID: principal.UserID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrRoomNotFound
		} else if err != nil {
			return err
		}
		_, err := repo.Queries.AcknowledgeRoomCloseNotice(ctx, roomsql.AcknowledgeRoomCloseNoticeParams{RoomID: roomID, RecipientUserID: principal.UserID})
		return err
	})
}

func (s *Service) CloseRoom(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeGetRoomError(w, r, ErrUnauthenticated)
		return
	}
	roomID, err := uuid.Parse(chi.URLParam(r, "roomId"))
	if err != nil || roomID == uuid.Nil {
		writeGetRoomError(w, r, ErrRoomNotFound)
		return
	}
	if err := s.Close(r.Context(), principal, roomID); err != nil {
		writeGetRoomError(w, r, err)
		return
	}
	snapshot, err := s.Get(r.Context(), principal, roomID)
	if err != nil {
		writeGetRoomError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, snapshot)
}

func (s *Service) AcknowledgeRoomCloseNotice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeGetRoomError(w, r, ErrUnauthenticated)
		return
	}
	roomID, err := uuid.Parse(chi.URLParam(r, "roomId"))
	if err != nil || roomID == uuid.Nil {
		writeGetRoomError(w, r, ErrRoomNotFound)
		return
	}
	if err := s.AcknowledgeCloseNotice(r.Context(), principal, roomID); err != nil {
		writeGetRoomError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
