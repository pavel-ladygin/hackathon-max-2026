package eventsources

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestResourceDomainAdminRoutesAreSourceScopedAndRequireMutationProtection(t *testing.T) {
	db := openEventSourcesTestDB(t)
	codec, err := sourceconfig.NewSecretCodec(bytes.Repeat([]byte{0x4f}, 32), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	keyA, keyB := "generic:domain-route-"+uuid.NewString(), "generic:domain-route-"+uuid.NewString()
	inputA := handlerTestInput(keyA, "", "event-a")
	inputA.AuthType = "none"
	sourceA, err := sources.Create(context.Background(), inputA)
	if err != nil {
		t.Fatal(err)
	}
	inputB := handlerTestInput(keyB, "", "event-b")
	inputB.AuthType = "none"
	sourceB, err := sources.Create(context.Background(), inputB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM event_sources WHERE id=ANY($1::uuid[])`, []uuid.UUID{sourceA.ID, sourceB.ID})
	})
	handler, err := NewHandler(context.Background(), db, sources)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	base := "/api/v1/internal/event-sources/" + sourceA.ID.String() + "/domains"

	missingProtection := doHandlerRequest(t, router, http.MethodPost, base, []byte(`{"hostname":"cdn.partner.ru","purpose":"image"}`), false)
	if missingProtection.Code != http.StatusForbidden {
		t.Fatalf("unprotected mutation status=%d, want 403", missingProtection.Code)
	}
	invalid := doHandlerRequest(t, router, http.MethodPost, base, []byte(`{"hostname":"*.partner.ru","purpose":"image"}`), true)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("wildcard approval status=%d, want 400", invalid.Code)
	}
	added := doHandlerRequest(t, router, http.MethodPost, base, []byte(`{"hostname":"CDN.Partner.RU","purpose":"image"}`), true)
	if added.Code != http.StatusOK || strings.Contains(added.Body.String(), "CDN.Partner.RU") {
		t.Fatalf("approval response status/body=%d %s", added.Code, added.Body.String())
	}
	var envelope struct {
		Domain struct {
			ID       uuid.UUID `json:"id"`
			Hostname string    `json:"hostname"`
			Purpose  string    `json:"purpose"`
		} `json:"domain"`
	}
	if err := json.Unmarshal(added.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Domain.ID == uuid.Nil || envelope.Domain.Hostname != "cdn.partner.ru" || envelope.Domain.Purpose != "image" {
		t.Fatalf("domain response=%+v", envelope.Domain)
	}
	list := doHandlerRequest(t, router, http.MethodGet, base, nil, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"hostname":"cdn.partner.ru"`) {
		t.Fatalf("domain list=%d %s", list.Code, list.Body.String())
	}
	crossSource := doHandlerRequest(t, router, http.MethodGet, "/api/v1/internal/event-sources/"+sourceB.ID.String()+"/domains", nil, false)
	if crossSource.Code != http.StatusOK || strings.Contains(crossSource.Body.String(), "cdn.partner.ru") {
		t.Fatalf("cross-source listing=%d %s", crossSource.Code, crossSource.Body.String())
	}
	revoked := doHandlerRequest(t, router, http.MethodDelete, base+"/"+envelope.Domain.ID.String(), nil, true)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	list = doHandlerRequest(t, router, http.MethodGet, base, nil, false)
	if strings.Contains(list.Body.String(), "cdn.partner.ru") {
		t.Fatalf("revoked domain remains listed: %s", list.Body.String())
	}
}
