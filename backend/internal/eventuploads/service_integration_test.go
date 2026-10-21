package eventuploads

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func TestUploadOwnershipClaimsExpirationCleanupAndQuota(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		t.Fatalf("check integration database migrations: %v", err)
	}

	owner, otherOwner := uuid.New(), uuid.New()
	city := uuid.New()
	for _, id := range []uuid.UUID{owner, otherOwner} {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,max_user_id,display_name) VALUES($1,$2,'Upload test')`, id, time.Now().UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Upload test','UTC',0,0)`, city); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []uuid.UUID{owner, otherOwner})
		_, _ = db.Exec(context.Background(), `DELETE FROM cities WHERE id=$1`, city)
	}()

	objects := &memoryObjects{items: make(map[string][]byte)}
	service := NewService(db, objects)
	pngData := tinyPNG(t)
	firstSubmission := insertUploadTestSubmission(t, db, owner, city)

	// The uploader UUID is accepted only for its owner, and a valid claim is
	// durable only when its surrounding submission transaction commits.
	upload, err := service.Store(ctx, owner, pngData, "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	wrongOwnerTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, wrongOwnerTx, upload.ID, otherOwner, firstSubmission); err != ErrNotOwner {
		t.Fatalf("claim by other owner = %v", err)
	}
	_ = wrongOwnerTx.Rollback(ctx)
	rollbackTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, rollbackTx, upload.ID, owner, firstSubmission); err != nil {
		t.Fatal(err)
	}
	_ = rollbackTx.Rollback(ctx)
	unclaimed, err := service.ByID(ctx, upload.ID)
	if err != nil || unclaimed.SubmissionID != nil {
		t.Fatalf("upload after rollback = %#v, %v", unclaimed, err)
	}
	claimTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, claimTx, upload.ID, owner, firstSubmission); err != nil {
		t.Fatal(err)
	}
	if err := claimTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	secondSubmission := insertUploadTestSubmission(t, db, owner, city)
	alreadyClaimedTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, alreadyClaimedTx, upload.ID, owner, secondSubmission); err != ErrClaimed {
		t.Fatalf("duplicate claim = %v", err)
	}
	_ = alreadyClaimedTx.Rollback(ctx)
	lookupTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ByIDTx(ctx, lookupTx, upload.ID); err != nil {
		t.Fatalf("transactional metadata lookup = %v", err)
	}
	_ = lookupTx.Rollback(ctx)

	expiredSubmission := insertUploadTestSubmission(t, db, owner, city)
	expiredUpload, err := service.Store(ctx, owner, pngData, "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE event_image_uploads SET created_at=now()-interval '25 hours' WHERE id=$1`, expiredUpload.ID); err != nil {
		t.Fatal(err)
	}
	expiredTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, expiredTx, expiredUpload.ID, owner, expiredSubmission); err != ErrExpired {
		t.Fatalf("expired claim = %v", err)
	}
	_ = expiredTx.Rollback(ctx)
	if removed, err := service.CleanupUnattached(ctx, 50); err != nil || removed != 1 {
		t.Fatalf("cleanup expired image = %d, %v", removed, err)
	}

	failedDeleteUpload, err := service.Store(ctx, owner, pngData, "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	attachedOldUpload, err := service.Store(ctx, owner, pngData, "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	attachedSubmission := insertUploadTestSubmission(t, db, owner, city)
	attachedTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim(ctx, attachedTx, attachedOldUpload.ID, owner, attachedSubmission); err != nil {
		t.Fatal(err)
	}
	if err := attachedTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE event_image_uploads SET created_at=now()-interval '25 hours' WHERE id = ANY($1::uuid[])`, []uuid.UUID{failedDeleteUpload.ID, attachedOldUpload.ID}); err != nil {
		t.Fatal(err)
	}
	objects.failDelete = true
	if removed, err := service.CleanupUnattached(ctx, 50); removed != 0 || err == nil {
		t.Fatalf("cleanup with failed object deletion = %d, %v", removed, err)
	}
	var remains, deleting bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_image_uploads WHERE id=$1), COALESCE((SELECT deleting FROM event_image_uploads WHERE id=$1), false)`, failedDeleteUpload.ID).Scan(&remains, &deleting); err != nil || !remains || deleting {
		t.Fatalf("failed deletion must release cleanup lease: remains=%v deleting=%v err=%v", remains, deleting, err)
	}
	objects.failDelete = false
	if removed, err := service.CleanupUnattached(ctx, 50); err != nil || removed != 1 {
		t.Fatalf("cleanup retry = %d, %v", removed, err)
	}
	if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_image_uploads WHERE id=$1)`, attachedOldUpload.ID).Scan(&remains); err != nil || !remains {
		t.Fatalf("cleanup removed attached upload: remains=%v err=%v", remains, err)
	}

	// The database row lock serializes a per-user cap with concurrent uploads;
	// the 21st unattached upload is rejected before an object is written.
	for i := 0; i < 20; i++ {
		if _, err := service.Store(ctx, otherOwner, pngData, "image/png", 1, 1); err != nil {
			t.Fatalf("store quota item %d: %v", i, err)
		}
	}
	before := objects.count()
	if _, err := service.Store(ctx, otherOwner, pngData, "image/png", 1, 1); err != ErrQuotaExceeded {
		t.Fatalf("21st pending upload = %v", err)
	}
	if after := objects.count(); after != before {
		t.Fatalf("quota failure stored object: before=%d after=%d", before, after)
	}
}

func insertUploadTestSubmission(t *testing.T, db *store.Pool, owner, city uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	key := uuid.NewString()
	if _, err := db.Exec(context.Background(), `INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,idempotency_key,request_hash)
		VALUES($1,$2,$3,now()+interval '2 days','UTC','{}'::jsonb,$4,$5)`, id, owner, city, key, bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	return id
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type memoryObjects struct {
	mu         sync.Mutex
	items      map[string][]byte
	failDelete bool
}

func (m *memoryObjects) Available() bool { return true }
func (m *memoryObjects) Put(_ context.Context, key string, body []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = append([]byte(nil), body...)
	return nil
}
func (m *memoryObjects) Get(_ context.Context, key string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.items[key]...), "image/png", nil
}
func (m *memoryObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDelete {
		return errors.New("simulated object-store outage")
	}
	delete(m.items, key)
	return nil
}
func (m *memoryObjects) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}
