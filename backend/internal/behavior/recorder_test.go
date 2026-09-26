package behavior

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
)

type recorderDB struct {
	statements []string
	insertErr  error
}

func (db *recorderDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	db.statements = append(db.statements, sql)
	if strings.Contains(sql, "INSERT INTO behavior_events") {
		return pgconn.CommandTag{}, db.insertErr
	}
	return pgconn.NewCommandTag("SAVEPOINT"), nil
}
func (*recorderDB) Query(context.Context, string, ...any) (pgx.Rows, error) { panic("unused") }
func (*recorderDB) QueryRow(context.Context, string, ...any) pgx.Row        { panic("unused") }

func TestRecorderFailureRollsBackToSavepointWithoutFailingProductFlow(t *testing.T) {
	roomID := uuid.New()
	db := &recorderDB{insertErr: errors.New("analytics unavailable")}
	err := (Recorder{}).Record(context.Background(), db, contracts.ServerBehaviorEvent{
		ID: uuid.New(), UserID: uuid.New(), Type: "room_created", RoomID: &roomID,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("analytics failure escaped to product flow: %v", err)
	}
	if len(db.statements) != 4 || db.statements[0] != "SAVEPOINT behavior_event_write" || !strings.Contains(db.statements[2], "ROLLBACK TO SAVEPOINT") || db.statements[3] != "RELEASE SAVEPOINT behavior_event_write" {
		t.Fatalf("unexpected savepoint recovery sequence: %#v", db.statements)
	}
}
