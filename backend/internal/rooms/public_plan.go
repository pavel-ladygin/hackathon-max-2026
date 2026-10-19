package rooms

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

// A published plan is deliberately independent of either participant's intent.
// Limits and enums match the existing intent contract, so it can prefill a form
// without submitting or confirming that form for either participant.
func validatePublicPlan(plan api.PublicPlan) error {
	if len(plan.Dates) < 1 || len(plan.Dates) > 14 || plan.TimeSlots == nil || len(plan.TimeSlots) > 4 || len(plan.CategorySlugs) < 1 || len(plan.CategorySlugs) > 11 || plan.BudgetMaxMinor < 0 || plan.BudgetMaxMinor > 100000000 {
		return ErrValidation
	}
	seen := make(map[string]struct{}, len(plan.Dates))
	for _, date := range plan.Dates {
		parsed, err := time.Parse(time.DateOnly, date)
		if err != nil || parsed.Format(time.DateOnly) != date || has(seen, date) {
			return ErrValidation
		}
		seen[date] = struct{}{}
	}
	if _, err := uniqueKnown(plan.TimeSlots, func(v api.TimeSlot) bool { return v.Valid() }); err != nil {
		return err
	}
	_, err := uniqueKnown(plan.CategorySlugs, func(v api.CategorySlug) bool { return v.Valid() })
	return err
}

func validatePublicPlanDates(plan api.PublicPlan, timezone string, now time.Time) error {
	zone, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" || timezone == "Local" {
		// Never substitute the process timezone for missing/invalid city data.
		return ErrValidation
	}
	today := now.In(zone).Format(time.DateOnly)
	for _, date := range plan.Dates {
		if date < today {
			return ErrValidation
		}
	}
	return nil
}

func decodePublicPlan(raw []byte) (api.PublicPlan, error) {
	var plan api.PublicPlan
	fields := make(map[string]json.RawMessage)
	keys := json.NewDecoder(bytes.NewReader(raw))
	if token, err := keys.Token(); err != nil || token != json.Delim('{') {
		return plan, ErrValidation
	}
	for keys.More() {
		token, err := keys.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return plan, ErrValidation
		}
		if _, duplicate := fields[key]; duplicate {
			return plan, ErrValidation
		}
		var value json.RawMessage
		if err := keys.Decode(&value); err != nil {
			return plan, ErrValidation
		}
		fields[key] = value
	}
	if _, err := keys.Token(); err != nil || len(fields) != 4 || !json.Valid(raw) {
		return plan, ErrValidation
	}
	for _, key := range []string{"dates", "time_slots", "category_slugs", "budget_max_minor"} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return plan, ErrValidation
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, ErrValidation
	}
	return plan, validatePublicPlan(plan)
}

// Canonicalize sets only for hashing. Keep the published list order in storage
// and responses; JSON property order and set order do not change idempotency.
func canonicalPublicPlan(plan api.PublicPlan) api.PublicPlan {
	plan.Dates = append([]string{}, plan.Dates...)
	plan.TimeSlots = append([]api.TimeSlot{}, plan.TimeSlots...)
	plan.CategorySlugs = append([]api.CategorySlug{}, plan.CategorySlugs...)
	sort.Strings(plan.Dates)
	sort.Slice(plan.TimeSlots, func(i, j int) bool { return plan.TimeSlots[i] < plan.TimeSlots[j] })
	sort.Slice(plan.CategorySlugs, func(i, j int) bool { return plan.CategorySlugs[i] < plan.CategorySlugs[j] })
	return plan
}

func storedPublicPlan(raw []byte) nullable.Nullable[api.PublicPlan] {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	// Only validated create writes this column. Fail closed if stored data was
	// changed outside the service: never reflect unexpected/private properties.
	plan, err := decodePublicPlan(raw)
	if err != nil {
		return nil
	}
	return nullable.NewNullableWithValue(plan)
}
