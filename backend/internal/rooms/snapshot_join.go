package rooms

import (
	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func joinSnapshot(room roomsql.Room, participants []roomsql.GetPublicParticipantsRow) api.RoomSnapshot {
	snapshot := createSnapshot(room, participants, InviteMaterial{})
	snapshot.Invite = nullable.NewNullNullable[api.RoomInvite]()
	snapshot.AllowedActions = []api.RoomSnapshotAllowedActions{api.EditIntent}
	return snapshot
}
