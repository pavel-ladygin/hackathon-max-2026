package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
)

func TestResourceDomainApprovalIsSourceAndPurposeScopedAndRevocable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	repository, err := resourcedomains.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	sourceA, sourceB := uuid.New(), uuid.New()
	keyA, keyB := "generic:domain-test-"+uuid.NewString(), "generic:domain-test-"+uuid.NewString()
	for _, source := range []struct {
		id  uuid.UUID
		key string
	}{{sourceA, keyA}, {sourceB, keyB}} {
		if _, err := db.Exec(ctx, `INSERT INTO event_sources(id,source_key,name,endpoint_url,key_version,default_category,default_timezone,default_currency,default_status) VALUES($1,$2,'Domain test','https://events.example.com/api',1,'other','UTC','RUB','published')`, source.id, source.key); err != nil {
			t.Fatalf("insert source fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=ANY($1::uuid[])`, []uuid.UUID{sourceA, sourceB})
	})

	image, err := repository.Add(ctx, sourceA, "CDN.Partner.RU", resourcedomains.PurposeImage)
	if err != nil {
		t.Fatal(err)
	}
	again, err := repository.Add(ctx, sourceA, "cdn.partner.ru", resourcedomains.PurposeImage)
	if err != nil || image.ID != again.ID || again.Hostname != "cdn.partner.ru" {
		t.Fatalf("duplicate approval = %+v, %v", again, err)
	}

	assertAllowed := func(source uuid.UUID, host string, purpose resourcedomains.Purpose, want bool) {
		t.Helper()
		got, err := repository.Allowed(ctx, source, host, purpose)
		if err != nil || got != want {
			t.Errorf("Allowed(%s,%q,%q) = %t, %v; want %t", source, host, purpose, got, err, want)
		}
	}
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeImage, true)
	assertAllowed(sourceB, "cdn.partner.ru", resourcedomains.PurposeImage, false)
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeTicket, false)

	if _, err := repository.Add(ctx, sourceA, "cdn.partner.ru", resourcedomains.PurposeTicket); err != nil {
		t.Fatal(err)
	}
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeImage, true)
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeTicket, true)
	if revoked, err := repository.Revoke(ctx, sourceA, image.ID); err != nil || !revoked {
		t.Fatalf("Revoke() = %t, %v; want true", revoked, err)
	}
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeImage, false)
	assertAllowed(sourceA, "cdn.partner.ru", resourcedomains.PurposeTicket, true)
	if allowed, err := repository.AllowedBySourceKey(ctx, keyA, "cdn.partner.ru", resourcedomains.PurposeTicket); err != nil || !allowed {
		t.Fatalf("AllowedBySourceKey() = %t, %v; want true", allowed, err)
	}
	if allowed, err := repository.AllowedBySourceKey(ctx, keyB, "cdn.partner.ru", resourcedomains.PurposeTicket); err != nil || allowed {
		t.Fatalf("cross-source AllowedBySourceKey() = %t, %v; want false", allowed, err)
	}
}
