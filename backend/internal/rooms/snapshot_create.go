package rooms

import (
	"github.com/oapi-codegen/nullable"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

func createSnapshot(room roomsql.Room, participants []roomsql.GetPublicParticipantsRow, material InviteMaterial) api.RoomSnapshot {
	items := make([]api.PublicParticipant, 0, len(participants))
	for _, participant := range participants {
		item := api.PublicParticipant{
			Id: participant.UserID, DisplayName: participant.DisplayName,
			Role: api.PublicParticipantRole(participant.Role), IntentReady: participant.IntentReady,
		}
		if participant.AvatarUrl.Valid {
			item.AvatarUrl = nullable.NewNullableWithValue(participant.AvatarUrl.String)
		} else {
			item.AvatarUrl = nullable.NewNullNullable[string]()
		}
		items = append(items, item)
	}
	invite := api.RoomInvite{Url: material.URL, MaxDeepLink: material.MaxDeepLink, ExpiresAt: material.ExpiresAt}
	return api.RoomSnapshot{
		Id: room.ID, Name: room.Name, CityId: room.CityID, State: api.RoomState(room.State),
		RoundNo: int(room.RoundNo), Version: int(room.Version), CreatedAt: room.CreatedAt.Time,
		ExpiresAt: room.ExpiresAt.Time, Participants: items,
		Invite:   nullable.NewNullableWithValue(invite),
		MyIntent: nullable.NewNullNullable[api.MyIntent](), Pool: nullable.NewNullNullable[api.PoolSummary](),
		Match:          nullable.NewNullNullable[api.MatchSummary](),
		AllowedActions: []api.RoomSnapshotAllowedActions{api.Invite, api.EditIntent},
	}
}
