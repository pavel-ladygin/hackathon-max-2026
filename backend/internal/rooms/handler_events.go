package rooms

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

func (s *Service) GetRoomEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeRoomEventsError(w, r, ErrUnauthenticated)
		return
	}
	roomID, err := uuid.Parse(chi.URLParam(r, "roomId"))
	if err != nil || roomID == uuid.Nil {
		writeRoomEventsError(w, r, ErrRoomNotFound)
		return
	}
	input, err := roomEventsInput(r)
	if err != nil {
		writeRoomEventsError(w, r, err)
		return
	}
	response, err := s.GetEvents(r.Context(), principal, roomID, input)
	if err != nil {
		writeRoomEventsError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func roomEventsInput(r *http.Request) (RoomEventsInput, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "limit" && key != "cursor" {
			return RoomEventsInput{}, ErrValidation
		}
		if len(query[key]) != 1 {
			return RoomEventsInput{}, ErrValidation
		}
	}
	input := RoomEventsInput{}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxRoomEventsLimit {
			return RoomEventsInput{}, ErrValidation
		}
		input.Limit = limit
	}
	input.Cursor = query.Get("cursor")
	if _, present := query["cursor"]; present && input.Cursor == "" {
		return RoomEventsInput{}, ErrValidation
	}
	return input, nil
}

func writeRoomEventsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrValidation), errors.Is(err, ErrInvalidRoomEventsCursor):
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid room events request"})
	case errors.Is(err, ErrUnauthenticated):
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"})
	case errors.Is(err, ErrRoomNotFound):
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Room not found"})
	case errors.Is(err, ErrRoomClosed):
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusConflict, Code: "ROOM_CLOSED", Message: "Room is closed"})
	case errors.Is(err, ErrPoolNotReady):
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusConflict, Code: "POOL_NOT_READY", Message: "Room pool is not ready"})
	case errors.Is(err, ErrPoolExhausted):
		state := poolExhaustedState{MyPoolFinished: true}
		_ = errors.As(err, &state)
		writeRoomEventsExhausted(w, r, state)
	default:
		httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"})
	}
}

func writeRoomEventsExhausted(w http.ResponseWriter, r *http.Request, state poolExhaustedState) {
	type details struct {
		MyPoolFinished bool `json:"my_pool_finished"`
		RoomExhausted  bool `json:"room_exhausted"`
	}
	type body struct {
		Error struct {
			Code      string  `json:"code"`
			Message   string  `json:"message"`
			RequestID string  `json:"request_id"`
			Details   details `json:"details"`
		} `json:"error"`
	}
	response := body{}
	response.Error.Code = "POOL_EXHAUSTED"
	response.Error.Message = "Room pool is exhausted"
	response.Error.RequestID = httpapi.RequestID(r.Context())
	response.Error.Details = details{MyPoolFinished: state.MyPoolFinished, RoomExhausted: state.RoomExhausted}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(response)
}
