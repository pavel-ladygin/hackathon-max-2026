package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/saved"
)

// TestSavedEventsPersistenceContract exercises the rows used by the saved
// events projection against a real PostgreSQL database. The HTTP tests cover
// request authentication and envelope mapping; this test keeps the database
// invariants explicit and independent of a particular repository shape.
func TestSavedEventsPersistenceContract(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue := uuid.New(), uuid.New()
	users := []uuid.UUID{uuid.New(), uuid.New()}
	events := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	created := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id = ANY($1::uuid[])", users)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id = ANY($1::uuid[])", events)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id = $1", venue)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id = $1", city)
	})

	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng)
VALUES ($1,'Saved events test city','UTC',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type)
VALUES ($1,$2,'Saved events test venue','Test address',55.75,37.61,'concert_hall')`, venue, city); err != nil {
		t.Fatal(err)
	}
	for i, user := range users {
		if _, err := db.Exec(ctx, `INSERT INTO users (id,max_user_id,display_name,city_id)
VALUES ($1,$2,$3,$4)`, user, time.Now().UnixNano()+int64(i), "Saved events test user", city); err != nil {
			t.Fatal(err)
		}
	}
	for i, eventID := range events {
		if _, err := db.Exec(ctx, `INSERT INTO events
(id,source,external_id,is_demo,title,description,venue_id,starts_at,timezone,price_from_minor,currency,ticket_available,status)
VALUES ($1,'saved-test',$2,false,$3,'public event description',$4,$5,'UTC',100,'RUB',true,'published')`,
			eventID, eventID.String(), "Saved event "+eventID.String(), venue, created.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO event_categories (event_id,category_slug,is_primary)
VALUES ($1,'concerts',true)`, eventID); err != nil {
			t.Fatal(err)
		}
	}

	service, err := saved.NewService(db, behavior.Recorder{}, []byte("saved-events-integration-cursor-key"))
	if err != nil {
		t.Fatal(err)
	}
	// Two first-time desired-state saves race on the same unique key. Both
	// callers must observe the committed row and its identical timestamp.
	start := make(chan struct{})
	results := make(chan saved.State, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			state, err := service.Set(ctx, users[0], events[0], true)
			results <- state
			errors <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	err1, err2 := <-errors, <-errors
	if err1 != nil || err2 != nil || !first.Saved || !second.Saved || first.SavedAt == nil || second.SavedAt == nil || !first.SavedAt.Equal(*second.SavedAt) {
		t.Fatalf("concurrent saves = %#v, %#v; errors=%v,%v", first, second, err1, err2)
	}
	var savedRows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM saved_events WHERE user_id=$1 AND event_id=$2`, users[0], events[0]).Scan(&savedRows); err != nil {
		t.Fatal(err)
	}
	if savedRows != 1 {
		t.Fatalf("saved row count after concurrent saves = %d, want 1", savedRows)
	}
	var saveSignals int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1 AND type='save' AND event_id=$2`, users[0], events[0]).Scan(&saveSignals); err != nil {
		t.Fatal(err)
	}
	if saveSignals != 1 {
		t.Fatalf("concurrent/repeated save behavior rows = %d, want 1", saveSignals)
	}
	if _, err := service.Set(ctx, users[0], events[1], true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(ctx, users[0], uuid.New(), true); err != saved.ErrNotFound {
		t.Fatalf("unknown event save error = %v, want ErrNotFound", err)
	}
	// The same event saved by another user must remain isolated.
	if _, err := service.Set(ctx, users[1], events[2], true); err != nil {
		t.Fatal(err)
	}

	page, err := service.List(ctx, users[0], saved.ListInput{Tab: saved.TabSaved, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Event.ID != events[1] || page.Items[1].Event.ID != events[0] {
		t.Fatalf("user 1 saved events = %#v, want newest-first %v", page.Items, []uuid.UUID{events[1], events[0]})
	}
	for _, item := range page.Items {
		if item.SavedAt == nil || item.SavedAt.IsZero() {
			t.Fatalf("saved item has invalid saved_at: %#v", item)
		}
		if item.Match != nil {
			t.Fatalf("saved tab leaked match: %#v", item.Match)
		}
	}
	// Cursor traversal with a one-item page must be exclusive and complete.
	var traversed []uuid.UUID
	var cursor *saved.Cursor
	for len(traversed) < 3 {
		page, err := service.List(ctx, users[0], saved.ListInput{Tab: saved.TabSaved, Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			break
		}
		traversed = append(traversed, page.Items[0].Event.ID)
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	if len(traversed) != 2 || traversed[0] == traversed[1] || traversed[0] != events[1] || traversed[1] != events[0] {
		t.Fatalf("cursor traversal = %v, want %v", traversed, []uuid.UUID{events[1], events[0]})
	}

	var otherUserCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM saved_events WHERE user_id=$1`, users[1]).Scan(&otherUserCount); err != nil {
		t.Fatal(err)
	}
	if otherUserCount != 1 {
		t.Fatalf("user 2 saved count = %d, want 1", otherUserCount)
	}

	unsaved, err := service.Set(ctx, users[0], events[0], false)
	if err != nil || unsaved.Saved || unsaved.SavedAt != nil {
		t.Fatalf("unsave = %#v, err=%v; want saved=false and null saved_at", unsaved, err)
	}
	if _, err := service.Set(ctx, users[0], events[0], false); err != nil {
		t.Fatal(err)
	}
	var unsaveSignals int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1 AND type='unsave' AND event_id=$2`, users[0], events[0]).Scan(&unsaveSignals); err != nil {
		t.Fatal(err)
	}
	if unsaveSignals != 1 {
		t.Fatalf("repeated unsave behavior rows = %d, want 1", unsaveSignals)
	}
	page, err = service.List(ctx, users[0], saved.ListInput{Tab: saved.TabSaved, Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Event.ID != events[1] {
		t.Fatalf("after unsave page = %#v, err=%v", page, err)
	}
}
