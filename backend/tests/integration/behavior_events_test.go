package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

func TestBehaviorClientIngestDedupeAtomicityAndIdentity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue, event := uuid.New(), uuid.New(), uuid.New()
	users := []uuid.UUID{uuid.New(), uuid.New()}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id = ANY($1::uuid[])", users)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id = $1", event)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id = $1", venue)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id = $1", city)
	})
	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'Behavior test city','UTC',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,'Behavior test venue','Address',55.75,37.61,'concert_hall')`, venue, city); err != nil {
		t.Fatal(err)
	}
	for i, user := range users {
		if _, err := db.Exec(ctx, `INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,$3,$4)`, user, time.Now().UnixNano()+int64(i), "Behavior test user", city); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO events (id,source,external_id,title,description,venue_id,starts_at,timezone,currency,ticket_available,status) VALUES ($1,'behavior-test',$2,'Behavior event','Description',$3,$4,'UTC','RUB',false,'published')`, event, event.String(), venue, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	service, err := behavior.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	when := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	valid := behavior.ClientEvent{ClientEventID: "same-client-id", Type: "impression", OccurredAt: when, EventID: pgtype.UUID{Bytes: event, Valid: true}, Surface: pgtype.Text{String: "feed", Valid: true}, Position: pgtype.Int4{Int32: 0, Valid: true}}
	got, err := service.Ingest(ctx, users[0], []behavior.ClientEvent{valid})
	if err != nil || got.Accepted != 1 || got.Duplicates != 0 {
		t.Fatalf("first ingest=%#v err=%v", got, err)
	}
	if got, err := service.Ingest(ctx, users[0], []behavior.ClientEvent{
		{ClientEventID: "open-client-id", Type: "open", OccurredAt: when, EventID: pgtype.UUID{Bytes: event, Valid: true}, Surface: pgtype.Text{String: "detail", Valid: true}, RequestID: pgtype.Text{String: "request-open", Valid: true}},
		{ClientEventID: "share-client-id", Type: "share", OccurredAt: when, EventID: pgtype.UUID{Bytes: event, Valid: true}, Surface: pgtype.Text{String: "detail", Valid: true}},
	}); err != nil || got.Accepted != 2 {
		t.Fatalf("open/share ingest=%#v err=%v", got, err)
	}
	got, err = service.Ingest(ctx, users[0], []behavior.ClientEvent{valid})
	if err != nil || got.Accepted != 0 || got.Duplicates != 1 {
		t.Fatalf("same-user duplicate=%#v err=%v", got, err)
	}
	got, err = service.Ingest(ctx, users[1], []behavior.ClientEvent{valid})
	if err != nil || got.Accepted != 1 {
		t.Fatalf("cross-user client id=%#v err=%v", got, err)
	}

	start := make(chan struct{})
	results := make(chan behavior.Result, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, ingestErr := service.Ingest(ctx, users[0], []behavior.ClientEvent{{ClientEventID: "concurrent", Type: "open", OccurredAt: when}})
			results <- result
			errs <- ingestErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	accepted, duplicates := 0, 0
	for result := range results {
		accepted += result.Accepted
		duplicates += result.Duplicates
	}
	for ingestErr := range errs {
		if ingestErr != nil {
			t.Fatal(ingestErr)
		}
	}
	if accepted != 1 || duplicates != 1 {
		t.Fatalf("concurrent dedupe accepted/duplicates=%d/%d", accepted, duplicates)
	}

	unknown := behavior.ClientEvent{ClientEventID: "unknown-event", Type: "open", OccurredAt: when, EventID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}
	_, err = service.Ingest(ctx, users[0], []behavior.ClientEvent{valid, unknown})
	if !errors.Is(err, behavior.ErrInvalid) {
		t.Fatalf("unknown event error=%v, want ErrInvalid", err)
	}
	var rows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1`, users[0]).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 4 {
		t.Fatalf("atomic invalid batch left %d rows, want 4", rows)
	}
	var metadataRows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1 AND origin='client' AND ((type='impression' AND surface='feed' AND position=0) OR (type='open' AND surface='detail' AND request_id='request-open') OR (type='share' AND surface='detail'))`, users[0]).Scan(&metadataRows); err != nil {
		t.Fatal(err)
	}
	if metadataRows != 3 {
		t.Fatalf("valid impression/open/share metadata rows=%d, want 3", metadataRows)
	}
}

func TestBehaviorRecorderPersistsAndValidatesServerEvents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue, event, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", user)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id = $1", event)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id = $1", venue)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id = $1", city)
	})
	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'Recorder city','UTC',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,'Recorder venue','Address',55.75,37.61,'cafe')`, venue, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,'Recorder user',$3)`, user, time.Now().UnixNano(), city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events (id,source,external_id,title,description,venue_id,starts_at,timezone,currency,status) VALUES ($1,'recorder-test',$2,'Recorder event','Description',$3,$4,'UTC','RUB','published')`, event, event.String(), venue, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	recorder := behavior.Recorder{}
	eventID := event
	serverEvent := contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: user, Type: "save", EventID: &eventID, RequestID: "request-1", OccurredAt: time.Now().UTC()}
	invalidEvent := contracts.ServerBehaviorEvent{ID: uuid.New(), UserID: user, Type: "unknown", OccurredAt: time.Now().UTC()}
	// Recorder uses a savepoint so analytics write errors can be isolated from
	// the surrounding product transaction.
	if err := db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := recorder.Record(ctx, tx, serverEvent); err != nil {
			return err
		}
		return recorder.Record(ctx, tx, invalidEvent)
	}); err != nil {
		t.Fatalf("recording behavior events failed: %v", err)
	}
	var rejectedRows int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE id=$1`, invalidEvent.ID).Scan(&rejectedRows); err != nil {
		t.Fatal(err)
	}
	if rejectedRows != 0 {
		t.Fatal("invalid server event was persisted")
	}
	var origin, typ string
	if err := db.QueryRow(ctx, `SELECT origin,type FROM behavior_events WHERE id=$1`, serverEvent.ID).Scan(&origin, &typ); err != nil {
		t.Fatal(err)
	}
	if origin != "server" || typ != "save" {
		t.Fatalf("recorded origin/type=%q/%q", origin, typ)
	}
}
