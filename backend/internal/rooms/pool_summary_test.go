package rooms

import (
	"testing"

	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
)

func TestPoolExhaustionReasonsAllowOnlyCanonicalDiagnostics(t *testing.T) {
	reasons := poolExhaustionReasons([]byte(`{"reasons":[{"code":"budget","text":"Слишком строгий бюджет."},{"code":"catalog_shortage","text":"Недостаточно событий."}]}`))
	if reasons == nil || len(*reasons) != 2 || (*reasons)[0].Code != api.Budget || (*reasons)[1].Code != api.CatalogShortage {
		t.Fatalf("reasons = %+v", reasons)
	}
}

func TestPoolExhaustionReasonsRejectUnsafeDiagnostics(t *testing.T) {
	for name, raw := range map[string][]byte{
		"malformed":     []byte(`{"reasons":`),
		"unknown code":  []byte(`{"reasons":[{"code":"private","text":"secret"}]}`),
		"unknown field": []byte(`{"reasons":[],"private":"secret"}`),
		"trailing json": []byte(`{"reasons":[]} {}`),
		"invalid utf8":  append([]byte(`{"reasons":[{"code":"budget","text":"`), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			if got := poolExhaustionReasons(raw); got != nil {
				t.Fatalf("unsafe diagnostics exposed: %+v", *got)
			}
		})
	}
}
