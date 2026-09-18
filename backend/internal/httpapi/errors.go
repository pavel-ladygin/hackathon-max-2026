package httpapi

import (
	"encoding/json"
	"net/http"
)

// Error is a transport-safe API error.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e Error) Error() string { return e.Code + ": " + e.Message }

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// WriteJSON writes a JSON response with the supplied HTTP status.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// WriteError writes the canonical API error envelope.
func WriteError(w http.ResponseWriter, requestID string, apiError Error) {
	WriteJSON(w, apiError.Status, errorResponse{Error: errorBody{
		Code:      apiError.Code,
		Message:   apiError.Message,
		RequestID: requestID,
	}})
}
