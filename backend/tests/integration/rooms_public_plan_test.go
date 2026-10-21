package integration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/behavior"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/rooms"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func publicPlanForDate(date string) api.PublicPlan {
	return api.PublicPlan{
		Dates: []string{date}, TimeSlots: []api.TimeSlot{"evening"},
		CategorySlugs: []api.CategorySlug{"concerts"}, BudgetMaxMinor: 250000,
	}
}

func TestCreateRoomPublicPlanPersistsAndIsSharedInSnapshotAndPreview(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	plan := publicPlanForDate(time.Now().UTC().AddDate(0, 0, 2).Format(time.DateOnly))
	request := api.CreateRoomRequest{CityId: f.city, Name: "Public proposal", PublicPlan: nullable.NewNullableWithValue(plan)}
	created, err := newCreateService(t, db, behavior.Recorder{}).Create(ctx, contracts.Principal{UserID: f.creator}, "public-plan-create", request)
	if err != nil {
		t.Fatal(err)
	}
	createdPlan, err := created.Room.PublicPlan.Get()
	if err != nil || !reflect.DeepEqual(createdPlan, plan) {
		t.Fatalf("create snapshot plan=%+v err=%v; want %+v", createdPlan, err, plan)
	}

	var storedJSON string
	if err := db.QueryRow(ctx, `SELECT public_plan::text FROM rooms WHERE id=$1`, created.Room.Id).Scan(&storedJSON); err != nil {
		t.Fatal(err)
	}
	var stored, want any
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(plan)
	if err := json.Unmarshal(wantJSON, &want); err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored public plan=%v err=%v; want %v", stored, err, want)
	}

	svc := newCreateService(t, db, behavior.Recorder{})
	snapshot, err := svc.Get(ctx, contracts.Principal{UserID: f.creator}, created.Room.Id)
	if err != nil {
		t.Fatal(err)
	}
	snapshotPlan, err := snapshot.PublicPlan.Get()
	if err != nil || !reflect.DeepEqual(snapshotPlan, plan) {
		t.Fatalf("room snapshot plan=%+v err=%v; want %+v", snapshotPlan, err, plan)
	}
	if _, err := svc.Get(ctx, contracts.Principal{UserID: f.third}, created.Room.Id); !errors.Is(err, rooms.ErrRoomNotFound) {
		t.Fatalf("Get by non-member = %v; want room not found", err)
	}

	var membersBefore int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id=$1`, created.Room.Id).Scan(&membersBefore); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.ResolveInviteContext(ctx, f.member, created.Invite.Token)
	if err != nil || preview == nil || preview.Status != api.Joinable {
		t.Fatalf("invite preview=%+v err=%v; want joinable preview", preview, err)
	}
	previewPlan, err := preview.PublicPlan.Get()
	if err != nil || !reflect.DeepEqual(previewPlan, plan) {
		t.Fatalf("invite preview plan=%+v err=%v; want %+v", previewPlan, err, plan)
	}
	q := roomsql.New(db)
	privateIntent := intentParams(f, f.creator, "private intent marker", 1234)
	privateIntent.RoomID = created.Room.Id
	privateIntent.FreeText = pgtype.Text{String: "private intent marker", Valid: true}
	if _, err := q.UpsertRoomIntent(ctx, privateIntent); err != nil {
		t.Fatal(err)
	}
	preview, err = svc.ResolveInviteContext(ctx, f.member, created.Invite.Token)
	if err != nil || preview == nil {
		t.Fatalf("invite preview after private intent insert=%+v err=%v", preview, err)
	}
	var membersAfter int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM room_members WHERE room_id=$1`, created.Room.Id).Scan(&membersAfter); err != nil || membersAfter != membersBefore {
		t.Fatalf("preview membership count before=%d after=%d err=%v; preview must not join", membersBefore, membersAfter, err)
	}
	previewJSON, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"my_intent", "location_lat", "location_lng", "private intent marker"} {
		if strings.Contains(string(previewJSON), forbidden) {
			t.Errorf("invite preview leaked %q: %s", forbidden, previewJSON)
		}
	}

	joined, err := svc.Join(ctx, contracts.Principal{UserID: f.member}, created.Invite.Token, "public-plan-join")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := joined.PublicPlan.Get(); err != nil || !reflect.DeepEqual(got, plan) {
		t.Fatalf("join snapshot plan=%+v err=%v; want unchanged %+v", got, err, plan)
	}
	updated, _, err := svc.ReplaceIntent(ctx, contracts.Principal{UserID: f.creator}, created.Room.Id, validIntentRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := updated.PublicPlan.Get(); err != nil || !reflect.DeepEqual(got, plan) {
		t.Fatalf("snapshot after own intent update plan=%+v err=%v; want unchanged %+v", got, err, plan)
	}

	fullPreview, err := svc.ResolveInviteContext(ctx, f.third, created.Invite.Token)
	if err != nil || fullPreview == nil || fullPreview.Status != api.Full || !publicPlanIsAbsent(fullPreview.PublicPlan) {
		t.Fatalf("full invite preview=%+v err=%v; public plan must be omitted", fullPreview, err)
	}
	if _, err := db.Exec(ctx, `UPDATE room_invites SET expires_at=clock_timestamp()-interval '1 second' WHERE room_id=$1`, created.Room.Id); err != nil {
		t.Fatal(err)
	}
	expiredPreview, err := svc.ResolveInviteContext(ctx, f.third, created.Invite.Token)
	if err != nil || expiredPreview == nil || expiredPreview.Status != api.Expired || !publicPlanIsAbsent(expiredPreview.PublicPlan) {
		t.Fatalf("expired invite preview=%+v err=%v; public plan must be omitted", expiredPreview, err)
	}
	if _, err := db.Exec(ctx, `UPDATE room_invites SET expires_at=clock_timestamp()+interval '1 hour' WHERE room_id=$1`, created.Room.Id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(ctx, contracts.Principal{UserID: f.creator}, created.Room.Id); err != nil {
		t.Fatal(err)
	}
	closedPreview, err := svc.ResolveInviteContext(ctx, f.third, created.Invite.Token)
	if err != nil || closedPreview == nil || closedPreview.Status != api.Full || !publicPlanIsAbsent(closedPreview.PublicPlan) {
		t.Fatalf("closed invite preview=%+v err=%v; public plan must be omitted", closedPreview, err)
	}
}

func TestPublicPlanLegacyAndIdempotencySemantics(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	svc := newCreateService(t, db, behavior.Recorder{})

	legacyUser := f.newUser(t)
	legacyRequest := api.CreateRoomRequest{CityId: f.city, Name: "Legacy without plan"}
	legacy, err := svc.Create(ctx, contracts.Principal{UserID: legacyUser}, "legacy-public-plan", legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !publicPlanIsAbsent(legacy.Room.PublicPlan) {
		t.Fatalf("legacy create snapshot public_plan=%#v; want null", legacy.Room.PublicPlan)
	}
	var legacyColumn any
	if err := db.QueryRow(ctx, `SELECT public_plan FROM rooms WHERE id=$1`, legacy.Room.Id).Scan(&legacyColumn); err != nil || legacyColumn != nil {
		t.Fatalf("legacy stored plan=%#v err=%v; want SQL NULL", legacyColumn, err)
	}
	legacyRequest.PublicPlan = nullable.NewNullNullable[api.PublicPlan]()
	replayedLegacy, err := svc.Create(ctx, contracts.Principal{UserID: legacyUser}, "legacy-public-plan", legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	assertIdenticalJSON(t, legacy, replayedLegacy)
	legacyPreview, err := svc.ResolveInviteContext(ctx, f.third, legacy.Invite.Token)
	if err != nil || legacyPreview == nil || !publicPlanIsAbsent(legacyPreview.PublicPlan) {
		t.Fatal("legacy preview must not invent a plan", err)
	}

	// An absent plan and an explicit JSON null are the same legacy request.
	legacyNullUser := f.newUser(t)
	legacyRequest.PublicPlan = nullable.NewNullNullable[api.PublicPlan]()
	withNull, err := svc.Create(ctx, contracts.Principal{UserID: legacyNullUser}, "legacy-null-plan", legacyRequest)
	if err != nil || !publicPlanIsAbsent(withNull.Room.PublicPlan) {
		t.Fatalf("explicit-null legacy create plan=%#v err=%v; want success with null plan", withNull.Room.PublicPlan, err)
	}

	pastUser := f.newUser(t)
	pastPlan := publicPlanForDate(databaseNow(t, db).UTC().AddDate(0, 0, -1).Format(time.DateOnly))
	pastRequest := api.CreateRoomRequest{CityId: f.city, Name: "Past date", PublicPlan: nullable.NewNullableWithValue(pastPlan)}
	if _, err := svc.Create(ctx, contracts.Principal{UserID: pastUser}, "past-public-plan", pastRequest); !errors.Is(err, rooms.ErrValidation) {
		t.Fatalf("past public plan date error=%v; want validation", err)
	}

	planUser := f.newUser(t)
	plan := publicPlanForDate(time.Now().UTC().AddDate(0, 0, 3).Format(time.DateOnly))
	request := api.CreateRoomRequest{CityId: f.city, Name: "Replay plan", PublicPlan: nullable.NewNullableWithValue(plan)}
	first, err := svc.Create(ctx, contracts.Principal{UserID: planUser}, "public-plan-replay", request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.Create(ctx, contracts.Principal{UserID: planUser}, "public-plan-replay", request)
	if err != nil {
		t.Fatalf("same plan replay: %v", err)
	}
	assertIdenticalJSON(t, first, replayed)

	changed := plan
	changed.BudgetMaxMinor++
	request.PublicPlan = nullable.NewNullableWithValue(changed)
	if _, err := svc.Create(ctx, contracts.Principal{UserID: planUser}, "public-plan-replay", request); !errors.Is(err, rooms.ErrIdempotencyConflict) {
		t.Fatalf("same key with a different plan = %v; want idempotency conflict", err)
	}
}

func publicPlanIsAbsent(value nullable.Nullable[api.PublicPlan]) bool {
	return len(value) == 0
}

func TestPublicPlanReplayPrecedesCurrentCityDateValidation(t *testing.T) {
	db := openTestDB(t)
	f := newRoomFixture(t, db)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE cities SET timezone='Pacific/Pago_Pago' WHERE id=$1`, f.city); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Pacific/Pago_Pago")
	if err != nil {
		t.Fatal(err)
	}
	date := databaseNow(t, db).In(location).Format(time.DateOnly)
	plan := publicPlanForDate(date)
	request := api.CreateRoomRequest{CityId: f.city, Name: "Replay after timezone date change", PublicPlan: nullable.NewNullableWithValue(plan)}
	svc := newCreateService(t, db, behavior.Recorder{})
	first, err := svc.Create(ctx, contracts.Principal{UserID: f.creator}, "timezone-replay-plan", request)
	if err != nil {
		t.Fatalf("create on valid local date %s: %v", date, err)
	}
	if _, err := db.Exec(ctx, `UPDATE cities SET timezone='Pacific/Kiritimati' WHERE id=$1`, f.city); err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, contracts.Principal{UserID: f.creator}, "timezone-replay-plan", request)
	if err != nil {
		t.Fatalf("replay should return cached create after city-local date passes: %v", err)
	}
	assertIdenticalJSON(t, first, second)
}
