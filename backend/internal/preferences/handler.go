// Package preferences owns a user's persistent discovery preferences.
package preferences

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const maxPreferencesRequestBytes = 128 * 1024

// Replacer is the application boundary used by the preferences HTTP transport.
type Replacer interface {
	Replace(context.Context, uuid.UUID, Input) (Value, error)
}

type NotificationPreferenceUpdater interface {
	SetDailyNotificationsEnabled(context.Context, uuid.UUID, bool) (bool, error)
}

// Handler exposes the authenticated preferences endpoint.
type Handler struct {
	replacer      Replacer
	notifications NotificationPreferenceUpdater
}

// NewHandler constructs the preferences HTTP transport.
func NewHandler(replacer Replacer) *Handler {
	updater, _ := replacer.(NotificationPreferenceUpdater)
	return &Handler{replacer: replacer, notifications: updater}
}

// RegisterRoutes mounts the protected preferences endpoint. The supplied middleware
// is responsible for authenticating the request and placing contracts.Principal in
// its context.
func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.With(authenticate).Put("/api/v1/me/preferences", h.Replace)
	r.With(authenticate).Patch("/api/v1/me/notification-preferences", h.UpdateNotifications)
}

// Replace fully replaces the authenticated user's persistent preferences.
func (h *Handler) Replace(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writePreferencesError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}

	request, err := decodePreferencesRequest(w, r)
	if err != nil {
		writePreferencesError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid preferences request")
		return
	}

	value, err := h.replacer.Replace(r.Context(), principal.UserID, request)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writePreferencesError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid preferences request")
			return
		}
		writePreferencesError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, api.PreferencesResponse{
		CityId:         value.CityID,
		InterestSlugs:  toAPICategorySlugs(value.InterestSlugs),
		BudgetMaxMinor: value.BudgetMaxMinor,
		UsualDayTypes:  toAPIDayTypes(value.UsualDayTypes),
		UsualTimeSlots: toAPITimeSlots(value.UsualTimeSlots),
		Version:        value.Version,
		UpdatedAt:      value.UpdatedAt,
	})
}

func (h *Handler) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writePreferencesError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	if h.notifications == nil {
		writePreferencesError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	var request struct {
		DailyNotificationsEnabled *bool `json:"daily_notifications_enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.DailyNotificationsEnabled == nil {
		writePreferencesError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid notification preferences request")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writePreferencesError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid notification preferences request")
		return
	}
	enabled, err := h.notifications.SetDailyNotificationsEnabled(r.Context(), principal.UserID, *request.DailyNotificationsEnabled)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writePreferencesError(w, r, http.StatusNotFound, "NOT_FOUND", "User not found")
			return
		}
		writePreferencesError(w, r, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, api.NotificationPreferencesResponse{DailyNotificationsEnabled: enabled})
}

type preferencesRequest struct {
	CityID         *uuid.UUID `json:"city_id"`
	InterestSlugs  *[]string  `json:"interest_slugs"`
	BudgetMaxMinor *int       `json:"budget_max_minor"`
	UsualDayTypes  *[]string  `json:"usual_day_types"`
	UsualTimeSlots *[]string  `json:"usual_time_slots"`
}

func decodePreferencesRequest(w http.ResponseWriter, r *http.Request) (Input, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPreferencesRequestBytes))
	decoder.DisallowUnknownFields()
	var request preferencesRequest
	if err := decoder.Decode(&request); err != nil {
		return Input{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Input{}, errors.New("trailing JSON value")
	}
	if request.CityID == nil || *request.CityID == uuid.Nil || request.InterestSlugs == nil || request.BudgetMaxMinor == nil || request.UsualDayTypes == nil || request.UsualTimeSlots == nil {
		return Input{}, ErrInvalid
	}
	input := Input{
		CityID:         *request.CityID,
		InterestSlugs:  *request.InterestSlugs,
		BudgetMaxMinor: *request.BudgetMaxMinor,
		UsualDayTypes:  *request.UsualDayTypes,
		UsualTimeSlots: *request.UsualTimeSlots,
	}
	if !validPreferencesInput(input) {
		return Input{}, ErrInvalid
	}
	return input, nil
}

func validPreferencesInput(input Input) bool {
	return input.BudgetMaxMinor >= 0 && input.BudgetMaxMinor <= 100_000_000 &&
		len(input.InterestSlugs) >= 1 && len(input.InterestSlugs) <= 11 &&
		validUnique(input.InterestSlugs, validCategorySlug) &&
		validUnique(input.UsualDayTypes, validDayType) &&
		validUnique(input.UsualTimeSlots, validTimeSlot)
}

func validUnique(values []string, permitted func(string) bool) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !permitted(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validCategorySlug(value string) bool {
	switch value {
	case "concerts", "cinema", "theatre", "standup", "exhibitions", "sports", "food", "parties", "festivals", "walks", "other":
		return true
	}
	return false
}

func validDayType(value string) bool { return value == "weekday" || value == "weekend" }

func validTimeSlot(value string) bool {
	switch value {
	case "morning", "day", "evening", "night":
		return true
	}
	return false
}

func toAPICategorySlugs(values []string) []api.CategorySlug {
	result := make([]api.CategorySlug, len(values))
	for i, value := range values {
		result[i] = api.CategorySlug(value)
	}
	return result
}

func toAPIDayTypes(values []string) []api.DayType {
	result := make([]api.DayType, len(values))
	for i, value := range values {
		result[i] = api.DayType(value)
	}
	return result
}

func toAPITimeSlots(values []string) []api.TimeSlot {
	result := make([]api.TimeSlot, len(values))
	for i, value := range values {
		result[i] = api.TimeSlot(value)
	}
	return result
}

func writePreferencesError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}
