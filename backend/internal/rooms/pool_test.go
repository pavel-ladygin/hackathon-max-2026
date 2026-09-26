package rooms

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func TestParticipantIntentDoesNotPassStoredGeolocationToRoomPool(t *testing.T) {
	radius := int32(5000)
	intent := roomsql.RoomIntent{
		UserID:         uuid.New(),
		Version:        1,
		RadiusM:        pgtype.Int4{Int32: radius, Valid: true},
		LocationLat:    pgtype.Float8{Float64: 55.75, Valid: true},
		LocationLng:    pgtype.Float8{Float64: 37.61, Valid: true},
		BudgetMaxMinor: 350000,
	}

	got := participantIntent(intent)
	if got.RadiusM != nil || got.Location != nil {
		t.Fatalf("room intent passed geo constraints to pool builder: radius=%v location=%+v", got.RadiusM, got.Location)
	}
	if got.UserID != intent.UserID || got.BudgetMaxMinor != intent.BudgetMaxMinor {
		t.Fatalf("non-geo intent fields were not preserved: %+v", got)
	}
}
