package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPoolDiagnosticsNeverSerializeInternalCounts(t *testing.T) {
	diagnostics := ExhaustionDiagnostics{
		Reasons:  []ExhaustionReason{{Code: "catalog_shortage", Text: "Недостаточно подходящих событий в каталоге."}},
		Internal: &PoolBuildDiagnostics{CatalogTotal: 123456789, DateOverlap: 111222333},
	}
	got, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(ExhaustionDiagnostics{Reasons: diagnostics.Reasons})
	if err != nil || string(got) != string(want) {
		t.Fatalf("private counts entered room diagnostics: %s", got)
	}
	var restored ExhaustionDiagnostics
	if err := json.Unmarshal(got, &restored); err != nil || restored.Internal != nil {
		t.Fatal("room serialization restored private builder data")
	}
	// Match the strict decoder used by the room's public pool summary. Even a
	// malicious stored field named Internal must remain an unknown field.
	for _, raw := range []string{
		`{"reasons":[],"Internal":{"catalog_total":123456789}}`,
		`{"reasons":[],"internal":{"catalog_total":123456789}}`,
		`{"reasons":[],"all_future":123456789}`,
	} {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&restored); err == nil {
			t.Fatal("private field accepted by public decoder")
		}
	}
}
