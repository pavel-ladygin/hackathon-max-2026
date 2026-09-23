package integration

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
)

func TestRealCatalogProductionPoolBuilder(t *testing.T) {
	if os.Getenv("RUN_REAL_PROVIDER_ROOM_E2E") != "1" {
		t.Skip("set RUN_REAL_PROVIDER_ROOM_E2E=1 to use an imported real provider catalog")
	}
	db := openTestDB(t)
	ctx := context.Background()
	referenceTime := databaseNow(t, db)

	var cityID uuid.UUID
	var cityTimezone string
	if err := db.QueryRow(ctx, `
		SELECT id, timezone
		FROM cities
		WHERE name = 'Москва'
		LIMIT 1
	`).Scan(&cityID, &cityTimezone); err != nil {
		t.Fatalf("find Moscow: %v", err)
	}
	zone, err := time.LoadLocation(cityTimezone)
	if err != nil {
		t.Fatalf("load Moscow timezone %q: %v", cityTimezone, err)
	}
	var eligibleKudaGo, eligibleTimepad int
	if err := db.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE e.source='kudago'),
		count(*) FILTER (WHERE e.source='timepad')
		FROM events e JOIN venues v ON v.id=e.venue_id
		WHERE v.city_id=$1 AND e.is_demo=false AND e.provider_active=true
		  AND e.status='published' AND e.starts_at>$2
		  AND e.ticket_available=true AND nullif(trim(e.ticket_url),'') IS NOT NULL`, cityID, referenceTime).
		Scan(&eligibleKudaGo, &eligibleTimepad); err != nil {
		t.Fatal(err)
	}
	if eligibleKudaGo == 0 || eligibleTimepad == 0 {
		t.Fatalf("provider-neutral eligible catalog kudago=%d timepad=%d", eligibleKudaGo, eligibleTimepad)
	}

	var eventDate time.Time
	if err := db.QueryRow(ctx, `
		SELECT min(e.starts_at)
		FROM events e
		JOIN venues v ON v.id = e.venue_id
		WHERE v.city_id = $1
		  AND e.is_demo = false
		  AND e.source IN ('kudago', 'timepad')
		  AND e.status = 'published'
		  AND e.starts_at > $2
		  AND e.ticket_available = true
		  AND nullif(trim(e.ticket_url), '') IS NOT NULL
	`, cityID, referenceTime).Scan(&eventDate); err != nil {
		t.Fatalf("find real event date: %v", err)
	}

	builder, err := recommendations.NewPoolBuilderWithClock(
		catalog.NewRepository(db),
		[]byte("0123456789abcdef0123456789abcdef"),
		func() time.Time { return referenceTime },
	)
	if err != nil {
		t.Fatal(err)
	}

	input := contracts.BuildInput{
		RoomID:      uuid.New(),
		CityID:      cityID,
		RoundNo:     1,
		PoolVersion: 1,
		FirstIntent: contracts.ParticipantIntent{
			UserID:         uuid.New(),
			Dates:          []string{eventDate.In(zone).Format(time.DateOnly)},
			BudgetMaxMinor: 100000000,
		},
		SecondIntent: contracts.ParticipantIntent{
			UserID:         uuid.New(),
			Dates:          []string{eventDate.In(zone).Format(time.DateOnly)},
			BudgetMaxMinor: 100000000,
		},
	}

	result, err := builder.Build(ctx, input)
	if err != nil {
		t.Fatalf("production builder: %v", err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("production builder returned zero candidates from real catalog")
	}

	t.Logf(
		"builder returned %d candidates; ranker=%s; date=%s",
		len(result.Candidates),
		result.RankerVersion,
		eventDate.In(zone).Format(time.DateOnly),
	)

	for i, candidate := range result.Candidates {
		var source string
		var isDemo bool
		var providerActive, ticketAvailable bool
		var ticketURL string
		var startsAt time.Time
		var status string

		if err := db.QueryRow(ctx, `
			SELECT source, is_demo, provider_active, ticket_available, ticket_url, starts_at, status
			FROM events
			WHERE id = $1
		`, candidate.EventID).Scan(
			&source,
			&isDemo,
			&providerActive,
			&ticketAvailable,
			&ticketURL,
			&startsAt,
			&status,
		); err != nil {
			t.Fatalf("candidate %s lookup: %v", candidate.EventID, err)
		}

		if isDemo {
			t.Fatalf("candidate %s is demo", candidate.EventID)
		}
		if source != "kudago" && source != "timepad" {
			t.Fatalf("candidate %s has unexpected source %q", candidate.EventID, source)
		}
		if !providerActive || !ticketAvailable {
			t.Fatalf("candidate %s active=%t ticket_available=%t", candidate.EventID, providerActive, ticketAvailable)
		}
		if status != "published" {
			t.Fatalf("candidate %s status=%q", candidate.EventID, status)
		}
		if !startsAt.After(referenceTime) {
			t.Fatalf("candidate %s already started at %s", candidate.EventID, startsAt)
		}

		u, err := url.Parse(ticketURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			t.Fatalf("candidate %s has invalid CTA %q", candidate.EventID, ticketURL)
		}

		t.Logf(
			"candidate[%d] id=%s source=%s starts=%s CTA=%s",
			i,
			candidate.EventID,
			source,
			startsAt.Format(time.RFC3339),
			ticketURL,
		)
	}
}
