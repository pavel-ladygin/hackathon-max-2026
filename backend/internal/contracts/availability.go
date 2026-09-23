package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EventAvailability provides current catalog facts before voting/matching.
// Room-pool eligibility requires published status, TicketAvailable and a
// non-nil usable TicketURL. TicketAvailable reflects the strongest current
// signal exposed by each provider and does not universally guarantee ticket
// inventory. A free event has PriceFromMinor == 0; free events without an
// actionable provider CTA are not eligible for the MVP room pool.
// The caller also checks its hard budget; nil price means unknown, not free.
type EventAvailability interface {
	CheckForRoomVote(ctx context.Context, eventID uuid.UUID) (Availability, error)
}

type Availability struct {
	Exists          bool
	Status          string // published, sold_out or cancelled.
	StartsAt        time.Time
	PriceFromMinor  *int32
	PriceToMinor    *int32
	Currency        string
	TicketAvailable bool
	TicketURL       *string
}
