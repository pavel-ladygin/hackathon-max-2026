package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

var errInvalidRequest = errors.New("invalid bootstrap request")

// RegisterRoutes mounts the public bootstrap endpoint on the shared router.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.Post("/api/v1/auth/max/bootstrap", s.limitBootstrap(s.Bootstrap))
}

func (s *Service) Bootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var request api.BootstrapRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAuthError(w, r, errInvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeAuthError(w, r, errInvalidRequest)
		return
	}
	if request.InitData == "" || len(request.InitData) > 16384 {
		writeAuthError(w, r, errInvalidRequest)
		return
	}
	var hint *string
	if request.StartParam.IsSpecified() && !request.StartParam.IsNull() {
		value, err := request.StartParam.Get()
		if err != nil || utf8.RuneCountInString(value) > 256 {
			writeAuthError(w, r, errInvalidRequest)
			return
		}
		hint = &value
	}
	result, err := s.bootstrap(r.Context(), request.InitData, hint)
	if err != nil {
		writeAuthError(w, r, err)
		return
	}
	user := api.User{Id: result.user.ID, DisplayName: result.user.DisplayName, Locale: result.user.Locale}
	if result.user.AvatarUrl.Valid {
		user.AvatarUrl = nullable.NewNullableWithValue(result.user.AvatarUrl.String)
	}
	if result.user.CityID.Valid {
		user.CityId = nullable.NewNullableWithValue(uuid.UUID(result.user.CityID.Bytes))
	}
	response := api.BootstrapResponse{
		AccessToken: result.token, TokenType: "Bearer", ExpiresIn: int(SessionTTL.Seconds()),
		User: user, OnboardingState: api.BootstrapResponseOnboardingState(result.user.OnboardingState),
		Preferences:   nullable.NewNullNullable[api.PreferencesResponse](),
		InviteContext: nullable.NewNullNullable[api.InviteContext](),
	}
	httpapi.WriteJSON(w, http.StatusOK, response)
}

// Middleware protects feature routes using the foundation's single Principal/context contract.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			writeAuthError(w, r, ErrUnauthenticated)
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeAuthError(w, r, ErrUnauthenticated)
			return
		}
		principal, err := s.Authenticate(r.Context(), parts[1])
		if err != nil {
			writeAuthError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(contracts.WithPrincipal(r.Context(), principal)))
	})
}

func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := httpapi.Error{Status: http.StatusInternalServerError, Code: "INTERNAL", Message: "Internal server error"}
	switch {
	case errors.Is(err, errInvalidRequest):
		apiError = httpapi.Error{Status: http.StatusBadRequest, Code: "VALIDATION_FAILED", Message: "Invalid bootstrap request"}
	case errors.Is(err, ErrTokenExpired):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "TOKEN_EXPIRED", Message: "Session expired"}
	case errors.Is(err, ErrUnauthenticated):
		apiError = httpapi.Error{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required"}
	}
	if apiError.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), apiError)
}
