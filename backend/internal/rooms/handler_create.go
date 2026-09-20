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

const createRequestLimit = 16 * 1024

// RegisterRoutes mounts create-room. Authentication middleware is installed by
// the application around this feature router.
func (s *Service) RegisterRoutes(r chi.Router) { r.Post(createRoute, s.CreateRoom) }

func (s *Service) CreateRoom(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeCreateError(w, r, ErrUnauthenticated)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !utf8.ValidString(keys[0]) {
		writeCreateError(w, r, ErrValidation)
		return
	}
	request, err := decodeCreateRequest(w, r)
	if err != nil {
		writeCreateError(w, r, ErrValidation)
		return
	}
	response, err := s.Create(r.Context(), principal, keys[0], request)
	if err != nil {
		writeCreateError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, response)
}

func decodeCreateRequest(w http.ResponseWriter, r *http.Request) (api.CreateRoomRequest, error) {
	var request api.CreateRoomRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, createRequestLimit))
	if err != nil || !utf8.Valid(body) {
		return request, ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return request, ErrValidation
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return request, ErrValidation
	}
	if len(fields) != 2 || fields["name"] == nil || fields["city_id"] == nil {
		return request, ErrValidation
	}
	for field := range fields {
		if field != "name" && field != "city_id" {
			return request, ErrValidation
		}
	}
	if bytes.Equal(fields["name"], []byte("null")) || bytes.Equal(fields["city_id"], []byte("null")) {
		return request, ErrValidation
	}
	if err := json.Unmarshal(fields["name"], &request.Name); err != nil || !utf8.ValidString(request.Name) {
		return request, ErrValidation
	}
	if err := json.Unmarshal(fields["city_id"], &request.CityId); err != nil || request.CityId == uuid.Nil {
		return request, ErrValidation
	}
	return request, nil
}

func writeCreateError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, ErrValidation):
		apiError = httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid create room request"}
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	case errors.Is(err, ErrActiveRoomExists):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "ACTIVE_ROOM_EXISTS", Message: "An active room already exists"}
	case errors.Is(err, ErrIdempotencyConflict):
		apiError = httpapi.Error{Status: http.StatusConflict, Code: "IDEMPOTENCY_CONFLICT", Message: "Idempotency key was already used with another request"}
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}
