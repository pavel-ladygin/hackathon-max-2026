package rooms

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

const validPlanJSON = `{"dates":["2026-10-24"],"time_slots":["evening"],"category_slugs":["concerts"],"budget_max_minor":250000}`

func TestDecodeCreatePublicPlan(t *testing.T) {
	city := uuid.New()
	for _, plan := range []string{"", `,"public_plan":null`, `,"public_plan":` + validPlanJSON} {
		request, err := decodeCreateRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"name":"Room","city_id":%q%s}`, city, plan))))
		if err != nil {
			t.Fatal(err)
		}
		_, err = request.PublicPlan.Get()
		if (err == nil) != strings.Contains(plan, "dates") {
			t.Fatalf("unexpected plan presence: %q", plan)
		}
	}
	for name, plan := range map[string]string{
		"empty": `{}`, "array": `[]`, "string": `"private"`,
		"duplicate property": strings.Replace(validPlanJSON, `"budget_max_minor":250000`, `"budget_max_minor":0,"budget_max_minor":250000`, 1),
		"private":            strings.Replace(validPlanJSON, `"dates":`, `"my_intent":{},"dates":`, 1),
		"coordinates":        strings.Replace(validPlanJSON, `"dates":`, `"location":{"lat":55,"lng":37},"dates":`, 1),
		"unknown":            strings.Replace(validPlanJSON, `"dates":`, `"extra":true,"dates":`, 1),
		"missing budget":     strings.Replace(validPlanJSON, `,"budget_max_minor":250000`, "", 1),
		"null budget":        strings.Replace(validPlanJSON, "250000", "null", 1),
		"null dates":         strings.Replace(validPlanJSON, `["2026-10-24"]`, "null", 1),
		"null slots":         strings.Replace(validPlanJSON, `["evening"]`, "null", 1),
		"null categories":    strings.Replace(validPlanJSON, `["concerts"]`, "null", 1),
		"fraction":           strings.Replace(validPlanJSON, "250000", "1.5", 1),
		"exponent":           strings.Replace(validPlanJSON, "250000", "1e3", 1),
		"overflow":           strings.Replace(validPlanJSON, "250000", "999999999999999999999", 1),
		"budget string":      strings.Replace(validPlanJSON, "250000", `"250000"`, 1),
		"negative":           strings.Replace(validPlanJSON, "250000", "-1", 1),
		"above max":          strings.Replace(validPlanJSON, "250000", "100000001", 1),
		"empty dates":        strings.Replace(validPlanJSON, `["2026-10-24"]`, `[]`, 1),
		"duplicate dates":    strings.Replace(validPlanJSON, `["2026-10-24"]`, `["2026-10-24","2026-10-24"]`, 1),
		"date format":        strings.Replace(validPlanJSON, "2026-10-24", "2026-1-24", 1),
		"non date":           strings.Replace(validPlanJSON, "2026-10-24", "2026-02-30", 1),
		"timestamp":          strings.Replace(validPlanJSON, "2026-10-24", "2026-10-24T00:00:00Z", 1),
		"unknown slot":       strings.Replace(validPlanJSON, "evening", "lunch", 1),
		"duplicate slot":     strings.Replace(validPlanJSON, `["evening"]`, `["evening","evening"]`, 1),
		"unknown category":   strings.Replace(validPlanJSON, "concerts", "secret", 1),
		"duplicate category": strings.Replace(validPlanJSON, `["concerts"]`, `["concerts","concerts"]`, 1),
		"empty category":     strings.Replace(validPlanJSON, `["concerts"]`, `[]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"Room","city_id":%q,"public_plan":%s}`, city, plan)
			if _, err := decodeCreateRequest(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body))); err != ErrValidation {
				t.Fatalf("error = %v, want validation", err)
			}
		})
	}
}

func TestPublicPlanBoundariesAndTimezone(t *testing.T) {
	plan, err := decodePublicPlan([]byte(validPlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	plan.TimeSlots = []api.TimeSlot{}
	plan.Dates = nil
	for i := 1; i <= 14; i++ {
		plan.Dates = append(plan.Dates, fmt.Sprintf("2026-11-%02d", i))
	}
	for _, budget := range []int{0, 100000000} {
		plan.BudgetMaxMinor = budget
		if err := validatePublicPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	plan.Dates = append(plan.Dates, "2026-11-15")
	if validatePublicPlan(plan) != ErrValidation {
		t.Fatal("accepted >14 dates")
	}
	plan.Dates = []string{"2026-10-24"}
	now := time.Date(2026, 10, 24, 22, 30, 0, 0, time.UTC)
	if validatePublicPlanDates(plan, "Europe/Moscow", now) != ErrValidation {
		t.Fatal("accepted yesterday in Moscow")
	}
	if err := validatePublicPlanDates(plan, "America/Los_Angeles", now); err != nil {
		t.Fatal("rejected today in city timezone", err)
	}
	for _, zone := range []string{"", "Local", "No/Such_Zone"} {
		if validatePublicPlanDates(plan, zone, now) != ErrValidation {
			t.Fatalf("accepted timezone %q", zone)
		}
	}
	plan.Dates = []string{"2026-10-25"}
	if err := validatePublicPlanDates(plan, "Europe/Moscow", now); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPlanIdempotencyCanonicalizationAndLegacyHash(t *testing.T) {
	request := api.CreateRoomRequest{Name: "  Room  ", CityId: uuid.New()}
	_, legacyHash, err := normalizeCreate(request, "valid-key")
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(struct {
		Name   string    `json:"name"`
		CityID uuid.UUID `json:"city_id"`
	}{"Room", request.CityId})
	sum := sha256.Sum256(legacy)
	if legacyHash != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy hash changed")
	}
	request.PublicPlan = nullable.NewNullNullable[api.PublicPlan]()
	_, nullHash, _ := normalizeCreate(request, "valid-key")
	if nullHash != legacyHash {
		t.Fatal("null differs from omitted")
	}
	first := `{"budget_max_minor":250000,"category_slugs":["cinema","concerts"],"time_slots":["evening","day"],"dates":["2026-10-25","2026-10-24"]}`
	second := `{"dates":["2026-10-24","2026-10-25"],"time_slots":["day","evening"],"category_slugs":["concerts","cinema"],"budget_max_minor":250000}`
	var hashes []string
	for _, raw := range []string{first, second} {
		plan, err := decodePublicPlan([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.PublicPlan = nullable.NewNullableWithValue(plan)
		_, hash, err := normalizeCreate(request, "valid-key")
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
	}
	if hashes[0] != hashes[1] || hashes[0] == legacyHash {
		t.Fatal("incorrect plan canonicalization")
	}
	plan, _ := request.PublicPlan.Get()
	plan.BudgetMaxMinor++
	request.PublicPlan = nullable.NewNullableWithValue(plan)
	_, changed, _ := normalizeCreate(request, "valid-key")
	if changed == hashes[0] {
		t.Fatal("plan missing from hash")
	}
}

func TestStoredPublicPlanDoesNotReflectPrivateData(t *testing.T) {
	for _, raw := range []string{"", "null", `{}`, strings.Replace(validPlanJSON, `"dates":`, `"my_intent":{"location":{"lat":55}},"dates":`, 1)} {
		if _, err := storedPublicPlan([]byte(raw)).Get(); err == nil {
			t.Fatal("reflected invalid stored public plan")
		}
	}
	if _, err := storedPublicPlan([]byte(validPlanJSON)).Get(); err != nil {
		t.Fatal(err)
	}
}
