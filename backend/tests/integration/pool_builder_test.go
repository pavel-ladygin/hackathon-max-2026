package integration

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/recommendations"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestPoolBuilderSeededCatalog(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 18, 0, 0, 0, 0, zone)
	if _, err := catalogseed.Apply(ctx, db, base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupDemoCatalog(t, db) })

	f := newRoomFixture(t, db)
	f.addTwoMembers(t)
	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	if _, err := db.Exec(ctx, "UPDATE rooms SET city_id=$1 WHERE id=$2", cityID, f.room); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(context.Background(), "DELETE FROM rooms WHERE id=$1", f.room); err != nil {
			t.Error(err)
		}
	})
	for _, user := range []uuid.UUID{f.creator, f.member} {
		if _, err := roomsql.New(db).UpsertRoomIntent(ctx, intentParams(f, user, "private fixture text", 250000)); err != nil {
			t.Fatal(err)
		}
	}
	before := poolBuilderRoomSnapshot(t, db, f.room)
	t.Cleanup(func() {
		if after := poolBuilderRoomSnapshot(t, db, f.room); after != before {
			t.Error("PoolBuilder changed room state, intents, membership, pools, votes or matches")
		}
	})

	dates := make([]string, 14)
	for i := range dates {
		dates[i] = base.AddDate(0, 0, i+1).Format(time.DateOnly)
	}
	input := contracts.BuildInput{
		RoomID: f.room, CityID: cityID, RoundNo: 1, PoolVersion: 1,
		FirstIntent:  contracts.ParticipantIntent{UserID: f.creator, Version: 1, Dates: dates, BudgetMaxMinor: 250000, CategorySlugs: []string{"concerts"}},
		SecondIntent: contracts.ParticipantIntent{UserID: f.member, Version: 1, Dates: slices.Clone(dates), BudgetMaxMinor: 250000, CategorySlugs: []string{"theatre"}},
	}
	repo := catalog.NewRepository(db)
	builder, err := recommendations.NewPoolBuilder(repo, []byte("a4-postgres-integration-key-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	build := func(in contracts.BuildInput, count int) contracts.BuildResult {
		t.Helper()
		got, err := builder.Build(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Candidates) != count || got.IsSmall != (count > 0 && count < 3) {
			t.Fatalf("got %d candidates, is_small=%v; want %d", len(got.Candidates), got.IsSmall, count)
		}
		return got
	}

	full := build(input, 22)
	if repeated := build(input, 22); !reflect.DeepEqual(full, repeated) {
		t.Fatal("same effective input changed the result/order")
	}
	permuted := input
	permuted.FirstIntent.Dates = slices.Clone(dates)
	slices.Reverse(permuted.FirstIntent.Dates)
	permuted.FirstIntent.CategorySlugs = []string{"sports"}
	permuted.SecondIntent.CategorySlugs = []string{"food"}
	permutedResult := build(permuted, 22)
	if reflect.DeepEqual(full, permutedResult) {
		t.Fatal("changed category preferences did not affect ranking")
	}
	fullIDs, permutedIDs := candidateIDsFromResult(full), candidateIDsFromResult(permutedResult)
	slices.Sort(fullIDs)
	slices.Sort(permutedIDs)
	if !slices.Equal(fullIDs, permutedIDs) {
		t.Fatal("soft category preferences changed eligibility")
	}

	snapshot, err := repo.LoadCity(ctx, cityID)
	if err != nil {
		t.Fatal(err)
	}
	events := make(map[uuid.UUID]catalog.Event, len(snapshot.Events))
	for _, event := range snapshot.Events {
		events[event.ID] = event
	}
	preferredNearTop := 0
	for _, candidate := range full.Candidates[:4] {
		for _, category := range events[candidate.EventID].Categories {
			if category.CategorySlug == "concerts" || category.CategorySlug == "theatre" {
				preferredNearTop++
				break
			}
		}
	}
	if preferredNearTop < 2 {
		t.Fatalf("current category preferences did not rank both preferred categories near the top: %d", preferredNearTop)
	}
	allowedReasons := map[string]bool{"time_fit": true, "budget_fit": true, "shared_category": true, "nearby": true, "profile_affinity": true, "popular": true}
	for _, candidate := range full.Candidates {
		event := events[candidate.EventID]
		if event.Status != "published" || !event.TicketAvailable || !event.PriceFromMinor.Valid || event.PriceFromMinor.Int32 > 250000 {
			t.Fatalf("ineligible seeded event %s returned", candidate.EventID)
		}
		if candidate.Score.GroupScore < 0 || candidate.Score.GroupScore > 1 || len(candidate.FeatureSnapshot) != 7 || len(candidate.Explanation) > 3 {
			t.Fatalf("invalid ranking snapshot for %s: %+v", candidate.EventID, candidate)
		}
		for _, reason := range candidate.Explanation {
			if !allowedReasons[reason.Code] || reason.Text == "" || strings.Contains(reason.Text, "250000") {
				t.Fatalf("unsafe recommendation reason: %+v", reason)
			}
		}
	}

	freeInput := input
	freeInput.SecondIntent.BudgetMaxMinor = 0
	free := build(freeInput, 11)
	for _, candidate := range free.Candidates {
		if events[candidate.EventID].PriceFromMinor.Int32 != 0 {
			t.Fatal("zero budget admitted a paid event")
		}
	}
	evening := input
	evening.FirstIntent.TimeSlots = []string{"evening"}
	build(evening, 8)
	weekend := freeInput
	weekend.SecondIntent.DayTypes = []string{"weekend"}
	build(weekend, 4)
	oneDate := input
	oneDate.SecondIntent.Dates = []string{dates[0]}
	build(oneDate, 1)
	noSharedDate := oneDate
	noSharedDate.FirstIntent.Dates = []string{dates[1]}
	build(noSharedDate, 0)

	nearVenue := freeInput
	radius := int32(100)
	venue := snapshot.Venues[0]
	nearVenue.SecondIntent.RadiusM = &radius
	nearVenue.SecondIntent.Location = &contracts.GeoPoint{Latitude: venue.Latitude, Longitude: venue.Longitude}
	near, err := builder.Build(ctx, nearVenue)
	if err != nil || len(near.Candidates) == 0 || len(near.Candidates) >= len(free.Candidates) {
		t.Fatalf("radius did not narrow seeded pool: count=%d, err=%v", len(near.Candidates), err)
	}
	excluded := input
	excluded.SecondIntent.ExclusionSlugs = []string{"nightclubs", "very_loud", "outdoor", "far_from_metro"}
	filtered, err := builder.Build(ctx, excluded)
	if err != nil || len(filtered.Candidates) == 0 || len(filtered.Candidates) >= len(full.Candidates) {
		t.Fatalf("exclusions did not narrow seeded pool: count=%d, err=%v", len(filtered.Candidates), err)
	}

	next := input
	next.PoolVersion = 2
	for _, candidate := range full.Candidates[:20] {
		next.PreviousEventIDs = append(next.PreviousEventIDs, candidate.EventID)
	}
	rest := build(next, 2)
	for _, candidate := range rest.Candidates {
		if slices.Contains(next.PreviousEventIDs, candidate.EventID) {
			t.Fatal("previous event returned in the next pool")
		}
	}
	next.PreviousEventIDs = append(next.PreviousEventIDs, rest.Candidates[0].EventID)
	build(next, 1)
	next.PreviousEventIDs = append(next.PreviousEventIDs, rest.Candidates[1].EventID)
	empty := build(next, 0)
	if len(empty.Diagnostics.Reasons) == 0 {
		t.Fatal("empty pool has no safe exhaustion reason")
	}

	// A subsequent build must see current catalog availability, status and price.
	for i, assignment := range []string{"ticket_available=false", "status='sold_out'", "price_from_minor=NULL"} {
		if _, err := db.Exec(ctx, "UPDATE events SET "+assignment+" WHERE id=$1", free.Candidates[i].EventID); err != nil {
			t.Fatal(err)
		}
		build(freeInput, 10-i)
	}
}

func candidateIDsFromResult(result contracts.BuildResult) []string {
	ids := make([]string, len(result.Candidates))
	for i, candidate := range result.Candidates {
		ids[i] = candidate.EventID.String()
	}
	return ids
}

func poolBuilderRoomSnapshot(t *testing.T, db *store.Pool, roomID uuid.UUID) string {
	t.Helper()
	var snapshot string
	err := db.QueryRow(context.Background(), `SELECT jsonb_build_object(
		'room', (SELECT to_jsonb(r) FROM rooms r WHERE id=$1),
		'members', (SELECT jsonb_agg(to_jsonb(m) ORDER BY user_id) FROM room_members m WHERE room_id=$1),
		'intents', (SELECT jsonb_agg(to_jsonb(i) ORDER BY user_id,round_no) FROM room_intents i WHERE room_id=$1),
		'rounds', (SELECT jsonb_agg(to_jsonb(s) ORDER BY user_id,round_no) FROM room_member_round_state s WHERE room_id=$1),
		'pools', (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM room_pools p WHERE room_id=$1),
		'events', (SELECT jsonb_agg(to_jsonb(e) ORDER BY pool_id,position) FROM room_pool_events e JOIN room_pools p ON p.id=e.pool_id WHERE p.room_id=$1),
		'votes', (SELECT jsonb_agg(to_jsonb(v) ORDER BY pool_id,event_id,user_id) FROM room_votes v WHERE room_id=$1),
		'matches', (SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM room_matches m WHERE room_id=$1)
	)::text`, roomID).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
