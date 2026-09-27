package rooms

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

func (s *Service) JoinRoomByInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeJoinError(w, r, ErrUnauthenticated)
		return
	}
	token := chi.URLParam(r, "token")
	keys := r.Header.Values("Idempotency-Key")
	if !utf8.ValidString(token) || len([]rune(token)) < 16 || len([]rune(token)) > 256 || len(keys) != 1 || !utf8.ValidString(keys[0]) {
		writeJoinError(w, r, ErrValidation)
		return
	}
	snapshot, err := s.Join(r.Context(), principal, token, keys[0])
	if err != nil {
		writeJoinError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, snapshot)
}

func writeJoinError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, ErrValidation):
		apiError = httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid join request"}
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	case errors.Is(err, ErrInviteNotFound):
		apiError = httpapi.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Invite not found"}
	case errors.Is(err, ErrInviteExpired):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "INVITE_EXPIRED", Message: "Invite has expired"}
	case errors.Is(err, ErrRoomFull):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ROOM_FULL", Message: "Room is full"}
	case errors.Is(err, ErrRoomClosed):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ROOM_CLOSED", Message: "Room is closed"}
	case errors.Is(err, ErrActiveRoomExists):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ACTIVE_ROOM_EXISTS", Message: "An active room already exists"}
	case errors.Is(err, ErrIdempotencyConflict):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "IDEMPOTENCY_CONFLICT", Message: "Idempotency key was already used with another request"}
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}
