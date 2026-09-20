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

const voteRequestLimit = 4 * 1024

func (s *Service) VoteForRoomEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeRoomVoteError(w, r, ErrUnauthenticated)
		return
	}
	roomID, roomErr := uuid.Parse(chi.URLParam(r, "roomId"))
	eventID, eventErr := uuid.Parse(chi.URLParam(r, "eventId"))
	if roomErr != nil || eventErr != nil || roomID == uuid.Nil || eventID == uuid.Nil {
		writeRoomVoteError(w, r, ErrRoomNotFound)
		return
	}
	request, err := decodeVoteRequest(w, r)
	if err != nil {
		writeRoomVoteError(w, r, err)
		return
	}
	response, err := s.Vote(r.Context(), principal, roomID, eventID, request)
	if err != nil {
		writeRoomVoteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

func decodeVoteRequest(w http.ResponseWriter, r *http.Request) (api.VoteRequest, error) {
	var request api.VoteRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, voteRequestLimit))
	if err != nil || !utf8.Valid(body) {
		return request, ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, ErrValidation
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || request.PoolVersion < 1 || !request.Vote.Valid() {
		return request, ErrValidation
	}
	return request, nil
}

func writeRoomVoteError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, ErrValidation):
		apiError = httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid room vote request"}
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	case errors.Is(err, ErrRoomNotFound):
		apiError = httpapi.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Room event not found"}
	case errors.Is(err, ErrAlreadyMatched):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ALREADY_MATCHED", Message: "Room is already matched"}
	case errors.Is(err, ErrPoolNotReady):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "POOL_NOT_READY", Message: "Room pool is not ready"}
	case errors.Is(err, ErrPoolExhausted):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "POOL_EXHAUSTED", Message: "Room pool is exhausted"}
	case errors.Is(err, ErrStalePoolVersion):
		var stale stalePoolVersionError
		_ = errors.As(err, &stale)
		writeStalePoolVersion(w, r, stale.Current)
		return
	case errors.Is(err, ErrVoteAlreadyCast):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "VOTE_ALREADY_CAST", Message: "Vote was already cast"}
	case errors.Is(err, ErrEventUnavailable):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "EVENT_UNAVAILABLE", Message: "Event is no longer available"}
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}

func writeStalePoolVersion(w http.ResponseWriter, r *http.Request, current int) {
	type details struct {
		CurrentPoolVersion int `json:"current_pool_version"`
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
	response.Error.Code = "STALE_POOL_VERSION"
	response.Error.Message = "Room pool version is stale"
	response.Error.RequestID = httpapi.RequestID(r.Context())
	response.Error.Details.CurrentPoolVersion = current
	httpapi.WriteJSON(w, http.StatusConflict, response)
}
