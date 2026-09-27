package rooms

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const intentRequestLimit = 16 * 1024

func (s *Service) ReplaceMyRoomIntent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeIntentError(w, r, ErrUnauthenticated)
		return
	}
	roomID, err := uuid.Parse(chi.URLParam(r, "roomId"))
	if err != nil || roomID == uuid.Nil {
		writeIntentError(w, r, ErrRoomNotFound)
		return
	}
	request, err := decodeIntentRequest(w, r)
	if err != nil {
		writeIntentError(w, r, ErrValidation)
		return
	}
	snapshot, _, err := s.ReplaceIntent(r.Context(), principal, roomID, request)
	if err != nil {
		writeIntentError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, snapshot)
}

func decodeIntentRequest(w http.ResponseWriter, r *http.Request) (api.RoomIntentRequest, error) {
	var request api.RoomIntentRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, intentRequestLimit))
	if err != nil || !utf8.Valid(body) {
		return request, ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, ErrValidation
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return request, ErrValidation
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return request, ErrValidation
	}
	for _, field := range []string{"dates", "day_types", "time_slots", "category_slugs", "budget_max_minor", "exclusion_slugs"} {
		value, exists := fields[field]
		if !exists || bytes.Equal(value, []byte("null")) {
			return request, ErrValidation
		}
	}
	if raw, exists := fields["location"]; exists && !bytes.Equal(raw, []byte("null")) {
		var location map[string]json.RawMessage
		if err := json.Unmarshal(raw, &location); err != nil || location == nil {
			return request, ErrValidation
		}
		for _, field := range []string{"lat", "lng"} {
			coordinate, exists := location[field]
			if !exists || bytes.Equal(coordinate, []byte("null")) {
				return request, ErrValidation
			}
			var number float64
			if err := json.Unmarshal(coordinate, &number); err != nil {
				return request, ErrValidation
			}
		}
	}
	return request, nil
}

func writeIntentError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, ErrValidation):
		apiError = httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid room intent request"}
	case errors.Is(err, ErrPastIntentDate):
		apiError = httpapi.Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Message: "Intent dates cannot be in the past"}
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	case errors.Is(err, ErrRoomNotFound):
		apiError = httpapi.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Room not found"}
	case errors.Is(err, ErrAlreadyMatched):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ALREADY_MATCHED", Message: "Room is already matched"}
	case errors.Is(err, ErrRoundLimitReached):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ROUND_LIMIT_REACHED", Message: "Round limit reached"}
	case errors.Is(err, ErrIntentLocked):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "INTENT_LOCKED", Message: "Room intent is locked"}
	case errors.Is(err, ErrRoomClosed):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ROOM_CLOSED", Message: "Room is closed"}
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}
