package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/tickets"
)

func TestTicketClickValidatesAvailabilityURLAndRecordsOnlySuccess(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	city, venue, user := uuid.New(), uuid.New(), uuid.New()
	events := make([]uuid.UUID, 0, 7)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.Exec(cleanupCtx, "DELETE FROM users WHERE id=$1", user)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM events WHERE id=ANY($1::uuid[])", events)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM venues WHERE id=$1", venue)
		_, _ = db.Exec(cleanupCtx, "DELETE FROM cities WHERE id=$1", city)
	})
	if _, err := db.Exec(ctx, `INSERT INTO cities (id,name,timezone,center_lat,center_lng) VALUES ($1,'Ticket test city','UTC',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO venues (id,city_id,name,address,latitude,longitude,venue_type) VALUES ($1,$2,'Ticket venue','Address',55.75,37.61,'theatre')`, venue, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO users (id,max_user_id,display_name,city_id) VALUES ($1,$2,'Ticket user',$3)`, user, time.Now().UnixNano(), city); err != nil {
		t.Fatal(err)
	}

	type spec struct {
		status    string
		available bool
		url       *string
	}
	allowed := "https://tickets.example.test/event/ok"
	specs := []spec{
		{"published", true, &allowed},
		{"published", false, &allowed},
		{"published", true, nil},
		{"published", true, strptr("not a URL")},
		{"published", true, strptr("http://tickets.example.test/event")},
		{"published", true, strptr("https://tickets.example.test.attacker.test/event")},
		{"cancelled", true, &allowed},
	}
	for i, item := range specs {
		id := uuid.New()
		events = append(events, id)
		if _, err := db.Exec(ctx, `INSERT INTO events (id,source,external_id,title,description,venue_id,starts_at,timezone,currency,ticket_url,ticket_available,status) VALUES ($1,'ticket-test',$2,$3,'Description',$4,$5,'UTC','RUB',$6,$7,$8)`, id, id.String(), "Ticket event "+id.String(), venue, time.Now().Add(time.Duration(i+1)*time.Hour), item.url, item.available, item.status); err != nil {
			t.Fatal(err)
		}
	}
	service, err := tickets.NewService(db, behavior.Recorder{}, []string{"tickets.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Click(ctx, user, events[0])
	if err != nil || got != allowed {
		t.Fatalf("allowed click=%q err=%v", got, err)
	}
	var clicks int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1 AND type='ticket_click'`, user).Scan(&clicks); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Fatalf("ticket_click rows after success=%d, want 1", clicks)
	}
	for i, eventID := range events[1:] {
		_, err := service.Click(ctx, user, eventID)
		if !errors.Is(err, tickets.ErrUnavailable) {
			t.Errorf("case %d event %s error=%v, want ErrUnavailable", i+1, eventID, err)
		}
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM behavior_events WHERE user_id=$1 AND type='ticket_click'`, user).Scan(&clicks); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Fatalf("rejected clicks changed behavior rows=%d, want 1", clicks)
	}
	if _, err := service.Click(ctx, user, uuid.New()); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("unknown event error=%v", err)
	}
}

func strptr(value string) *string { return &value }
