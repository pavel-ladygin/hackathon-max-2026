// Package preferences persists the permanent event preferences of a user.
package preferences

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

var (
	// ErrInvalid indicates malformed preference input or an unknown city.
	ErrInvalid = errors.New("invalid preferences")
	// ErrNotFound indicates that the requested user no longer exists.
	ErrNotFound = errors.New("preferences user not found")
)

// Input is the complete permanent preference set supplied by a user.
type Input struct {
	CityID         uuid.UUID
	InterestSlugs  []string
	BudgetMaxMinor int
	UsualDayTypes  []string
	UsualTimeSlots []string
}

// Value is a persisted preference set.
type Value struct {
	CityID         uuid.UUID
	InterestSlugs  []string
	BudgetMaxMinor int
	UsualDayTypes  []string
	UsualTimeSlots []string
	Version        int
	UpdatedAt      time.Time
}

type repository interface {
	get(context.Context, uuid.UUID) (Value, bool, error)
	replace(context.Context, uuid.UUID, Input) (Value, error)
}

// Service validates and atomically replaces permanent preferences.
type Service struct{ repo repository }

// NewService returns a PostgreSQL-backed preference service.
func NewService(pool *store.Pool) *Service {
	return &Service{repo: NewRepository(pool)}
}

// Get returns found=false when the user has no saved preferences.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Value, bool, error) {
	if userID == uuid.Nil {
		return Value{}, false, ErrInvalid
	}
	return s.repo.get(ctx, userID)
}

// LoadRankingPreferences exposes only the provider-neutral projection needed
// by recommendation scoring.
func (s *Service) LoadRankingPreferences(ctx context.Context, userID uuid.UUID) (contracts.RankingPreferences, bool, error) {
	value, found, err := s.Get(ctx, userID)
	if err != nil || !found {
		return contracts.RankingPreferences{}, found, err
	}
	return contracts.RankingPreferences{
		CityID: value.CityID, InterestSlugs: append([]string(nil), value.InterestSlugs...),
		BudgetMaxMinor: int32(value.BudgetMaxMinor), UsualDayTypes: append([]string(nil), value.UsualDayTypes...),
		UsualTimeSlots: append([]string(nil), value.UsualTimeSlots...), Version: int32(value.Version),
	}, true, nil
}

// Replace validates input and replaces the entire persisted preference set.
func (s *Service) Replace(ctx context.Context, userID uuid.UUID, input Input) (Value, error) {
	if userID == uuid.Nil || !validInput(input) {
		return Value{}, ErrInvalid
	}
	return s.repo.replace(ctx, userID, cloneInput(input))
}

func validInput(input Input) bool {
	if input.CityID == uuid.Nil || len(input.InterestSlugs) < 1 || len(input.InterestSlugs) > 11 || input.BudgetMaxMinor < 0 || input.BudgetMaxMinor > 100000000 {
		return false
	}
	return uniqueAllowed(input.InterestSlugs, categorySlugs) &&
		uniqueAllowed(input.UsualDayTypes, dayTypes) &&
		uniqueAllowed(input.UsualTimeSlots, timeSlots)
}

func uniqueAllowed(values []string, allowed map[string]struct{}) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func cloneInput(input Input) Input {
	input.InterestSlugs = append([]string(nil), input.InterestSlugs...)
	// Empty optional arrays must remain PostgreSQL arrays rather than SQL NULL.
	input.UsualDayTypes = append([]string{}, input.UsualDayTypes...)
	input.UsualTimeSlots = append([]string{}, input.UsualTimeSlots...)
	return input
}

var categorySlugs = map[string]struct{}{
	"concerts": {}, "cinema": {}, "theatre": {}, "standup": {}, "exhibitions": {},
	"sports": {}, "food": {}, "parties": {}, "festivals": {}, "walks": {}, "other": {},
}

var dayTypes = map[string]struct{}{"weekday": {}, "weekend": {}}

var timeSlots = map[string]struct{}{"morning": {}, "day": {}, "evening": {}, "night": {}}
