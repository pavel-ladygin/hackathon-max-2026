package contracts

import (
	"context"

	"github.com/google/uuid"
)

// RankingPreferences is the provider-neutral permanent profile projection
// consumed by Backend A ranking. It is never exposed in room responses.
type RankingPreferences struct {
	CityID         uuid.UUID
	InterestSlugs  []string
	BudgetMaxMinor int32
	UsualDayTypes  []string
	UsualTimeSlots []string
	Version        int32
}

type RankingPreferencesLoader interface {
	LoadRankingPreferences(context.Context, uuid.UUID) (RankingPreferences, bool, error)
}

// PoolBuilder is a pure, deterministic at-least-once operation: Backend B may
// invoke it again with identical input after its surrounding transaction rolls
// back or retries. It reads catalog/profile data and returns a snapshot, but
// never reads or writes room state or performs any other side effect. Backend B
// supplies inputs, persists the result, changes room state and owns the
// surrounding transaction.
// A may load optional preferences from its own profile store using the supplied
// internal user IDs; B does not read or transfer A's private persistence models.
type PoolBuilder interface {
	Build(ctx context.Context, input BuildInput) (BuildResult, error)
}

type BuildInput struct {
	RoomID           uuid.UUID
	CityID           uuid.UUID
	RoundNo          int16
	PoolVersion      int32
	FirstIntent      ParticipantIntent
	SecondIntent     ParticipantIntent
	PreviousEventIDs []uuid.UUID // All earlier pool versions, not just the last round.
}

// ParticipantIntent is a private ranking input. Never log or expose it to the
// other participant. Dates are local YYYY-MM-DD values in the room city's zone.
type ParticipantIntent struct {
	UserID         uuid.UUID
	Version        int32
	Dates          []string
	DayTypes       []string
	TimeSlots      []string
	CategorySlugs  []string
	BudgetMaxMinor int32
	RadiusM        *int32
	Location       *GeoPoint
	ExclusionSlugs []string
	FreeText       *string
}

type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

type BuildResult struct {
	Candidates    []Candidate // Slice order is persisted as zero-based position.
	IsSmall       bool
	Diagnostics   ExhaustionDiagnostics
	RankerVersion string
	// Fingerprint covers all effective ranking inputs, including any profile
	// versions/data loaded by A. It must not expose raw private input values.
	InputFingerprint string
}

type Candidate struct {
	EventID         uuid.UUID
	Score           ScoreSnapshot
	Explanation     []Explanation
	FeatureSnapshot FeatureSnapshot
}

type ScoreSnapshot struct {
	GroupScore           float32
	ParticipantScoreMin  float32
	ParticipantScoreMean float32
}

// FeatureSnapshot contains aggregate numeric ranking features only. No raw
// intents, coordinates, free text, tokens or participant-specific scores.
type FeatureSnapshot map[string]float64

type Explanation struct {
	Code string `json:"code"` // Canonical RecommendationReason code.
	Text string `json:"text"` // Safe public explanation, without another user's data.
}

type ExhaustionDiagnostics struct {
	Reasons []ExhaustionReason `json:"reasons"`
}

type ExhaustionReason struct {
	Code string `json:"code"` // budget, date, categories, radius, catalog_shortage.
	Text string `json:"text"` // Aggregate only; must not reveal either private intent.
}
