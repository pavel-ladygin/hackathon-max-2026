package rooms

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

func (s *Service) GetRoom(w http.ResponseWriter, r *http.Request) {
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
	snapshot, err := s.Get(r.Context(), principal, roomID)
	if err != nil {
		writeGetRoomError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, snapshot)
}

func writeGetRoomError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	case errors.Is(err, ErrRoomNotFound):
		apiError = httpapi.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Room not found"}
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}
