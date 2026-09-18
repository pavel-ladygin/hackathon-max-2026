// Package recommendations builds deterministic, hard-filtered event pools.
package recommendations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const (
	rankerVersion = "hard-filters-v1"
	poolTarget    = 20
	metroLimitM   = 1200.0
	// distanceEpsM only absorbs floating-point noise at an inclusive boundary;
	// distances are never rounded before comparison.
	distanceEpsM = 1e-6
)

// Catalog provides the coherent, read-only city snapshot used to build a pool.
type Catalog interface {
	LoadCity(context.Context, uuid.UUID) (catalog.Snapshot, error)
}

// PoolBuilder applies room hard constraints. It has no room persistence or
// lifecycle responsibilities; Backend B owns those concerns.
type PoolBuilder struct {
	catalog Catalog
}

// NewPoolBuilder returns a contracts.PoolBuilder backed by catalog.
func NewPoolBuilder(catalog Catalog) *PoolBuilder { return &PoolBuilder{catalog: catalog} }

var _ contracts.PoolBuilder = (*PoolBuilder)(nil)

func (b *PoolBuilder) Build(ctx context.Context, input contracts.BuildInput) (contracts.BuildResult, error) {
	if b.catalog == nil {
		return contracts.BuildResult{}, errors.New("pool builder catalog is required")
	}
	if err := ctx.Err(); err != nil {
		return contracts.BuildResult{}, err
	}
	snapshot, err := b.catalog.LoadCity(ctx, input.CityID)
	if err != nil {
		return contracts.BuildResult{}, fmt.Errorf("load city catalog: %w", err)
	}
	if snapshot.City.ID != input.CityID {
		return contracts.BuildResult{}, errors.New("catalog snapshot city does not match input city")
	}
	first, err := normalizeIntent(input.FirstIntent)
	if err != nil {
		return contracts.BuildResult{}, fmt.Errorf("invalid first intent: %w", err)
	}
	second, err := normalizeIntent(input.SecondIntent)
	if err != nil {
		return contracts.BuildResult{}, fmt.Errorf("invalid second intent: %w", err)
	}
	zone, err := time.LoadLocation(snapshot.City.Timezone)
	if err != nil {
		return contracts.BuildResult{}, errors.New("catalog city has invalid timezone")
	}
	first.radius, err = effectiveRadius(first.radius, snapshot.City)
	if err != nil {
		return contracts.BuildResult{}, err
	}
	second.radius, err = effectiveRadius(second.radius, snapshot.City)
	if err != nil {
		return contracts.BuildResult{}, err
	}

	constraints := intersect(first, second)
	previous := uuidSet(input.PreviousEventIDs)
	venues := make(map[uuid.UUID]platform.Venue, len(snapshot.Venues))
	for _, venue := range snapshot.Venues {
		if venue.CityID == input.CityID {
			venues[venue.ID] = venue
		}
	}
	stations := make([]platform.MetroStation, 0, len(snapshot.MetroStations))
	for _, station := range snapshot.MetroStations {
		if station.CityID == input.CityID && validCoordinates(station.Latitude, station.Longitude) {
			stations = append(stations, station)
		}
	}

	type selected struct {
		event catalog.Event
		start time.Time
	}
	items := make([]selected, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		if err := ctx.Err(); err != nil {
			return contracts.BuildResult{}, err
		}
		if previous[event.ID] || !eligible(event, venues, stations, zone, constraints, first.radius, second.radius) {
			continue
		}
		items = append(items, selected{event: event, start: event.StartsAt.Time})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].start.Equal(items[j].start) {
			return items[i].event.ID.String() < items[j].event.ID.String()
		}
		return items[i].start.Before(items[j].start)
	})
	if len(items) > poolTarget {
		items = items[:poolTarget]
	}
	candidates := make([]contracts.Candidate, len(items))
	for i, item := range items {
		candidates[i] = contracts.Candidate{EventID: item.event.ID}
	}

	result := contracts.BuildResult{
		Candidates:       candidates,
		RankerVersion:    rankerVersion,
		InputFingerprint: fingerprint(input, constraints, snapshot.City.Timezone, first.radius, second.radius),
	}
	switch len(candidates) {
	case 0:
		result.Diagnostics.Reasons = []contracts.ExhaustionReason{{Code: "catalog_shortage", Text: "Недостаточно подходящих событий в каталоге."}}
	case 1, 2:
		result.IsSmall = true
	}
	return result, nil
}

type normalizedIntent struct {
	dates                   map[string]bool
	days, slots, exclusions map[string]bool
	budget                  int32
	radius                  radiusConstraint
}
type radiusConstraint struct {
	enabled, hasLocation bool
	meters               int32
	lat, lng             float64
}
type hardConstraints struct {
	dates, days, slots, exclusions map[string]bool
	budget                         int32
}

func normalizeIntent(in contracts.ParticipantIntent) (normalizedIntent, error) {
	dates := make(map[string]bool, len(in.Dates))
	for _, value := range in.Dates {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil || parsed.Format("2006-01-02") != value {
			return normalizedIntent{}, errors.New("invalid date")
		}
		dates[value] = true
	}
	if len(dates) == 0 {
		return normalizedIntent{}, errors.New("at least one date is required")
	}
	days, err := enumSet(in.DayTypes, map[string]bool{"weekday": true, "weekend": true})
	if err != nil {
		return normalizedIntent{}, errors.New("invalid day type")
	}
	slots, err := enumSet(in.TimeSlots, map[string]bool{"morning": true, "day": true, "evening": true, "night": true})
	if err != nil {
		return normalizedIntent{}, errors.New("invalid time slot")
	}
	exclusions, err := enumSet(in.ExclusionSlugs, map[string]bool{"nightclubs": true, "very_loud": true, "outdoor": true, "far_from_metro": true})
	if err != nil {
		return normalizedIntent{}, errors.New("invalid exclusion")
	}
	if in.BudgetMaxMinor < 0 {
		return normalizedIntent{}, errors.New("invalid budget")
	}
	r := radiusConstraint{}
	if in.RadiusM != nil {
		if *in.RadiusM < 0 {
			return normalizedIntent{}, errors.New("invalid radius")
		}
		r.enabled, r.meters = true, *in.RadiusM
		if in.Location != nil {
			if !validCoordinates(in.Location.Latitude, in.Location.Longitude) {
				return normalizedIntent{}, errors.New("invalid location")
			}
			r.hasLocation, r.lat, r.lng = true, in.Location.Latitude, in.Location.Longitude
		}
	}
	return normalizedIntent{dates: dates, days: days, slots: slots, exclusions: exclusions, budget: in.BudgetMaxMinor, radius: r}, nil
}

func enumSet(values []string, valid map[string]bool) (map[string]bool, error) {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if !valid[value] {
			return nil, errors.New("unknown value")
		}
		result[value] = true
	}
	return result, nil
}
func validCoordinates(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) && !math.IsInf(lat, 0) && !math.IsInf(lng, 0) && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}
func effectiveRadius(r radiusConstraint, city platform.City) (radiusConstraint, error) {
	if !r.enabled {
		return r, nil
	}
	if !r.hasLocation {
		r.lat, r.lng = city.CenterLat, city.CenterLng
	}
	if !validCoordinates(r.lat, r.lng) {
		return radiusConstraint{}, errors.New("catalog city has invalid center")
	}
	return r, nil
}

func intersect(a, b normalizedIntent) hardConstraints {
	return hardConstraints{
		dates:      setIntersection(a.dates, b.dates),
		days:       domainIntersection(a.days, b.days, []string{"weekday", "weekend"}),
		slots:      domainIntersection(a.slots, b.slots, []string{"morning", "day", "evening", "night"}),
		exclusions: setUnion(a.exclusions, b.exclusions),
		budget:     min(a.budget, b.budget),
	}
}
func setIntersection(a, b map[string]bool) map[string]bool {
	r := map[string]bool{}
	for x := range a {
		if b[x] {
			r[x] = true
		}
	}
	return r
}
func domainIntersection(a, b map[string]bool, domain []string) map[string]bool {
	if len(a) == 0 {
		a = domainSet(domain)
	}
	if len(b) == 0 {
		b = domainSet(domain)
	}
	return setIntersection(a, b)
}
func domainSet(values []string) map[string]bool {
	r := make(map[string]bool, len(values))
	for _, value := range values {
		r[value] = true
	}
	return r
}
func setUnion(a, b map[string]bool) map[string]bool {
	r := copySet(a)
	for x := range b {
		r[x] = true
	}
	return r
}
func copySet(a map[string]bool) map[string]bool {
	r := make(map[string]bool, len(a))
	for x := range a {
		r[x] = true
	}
	return r
}
func eligible(event catalog.Event, venues map[uuid.UUID]platform.Venue, stations []platform.MetroStation, zone *time.Location, c hardConstraints, radii ...radiusConstraint) bool {
	venue, ok := venues[event.VenueID]
	if !ok || event.Status != "published" || !event.TicketAvailable || !event.StartsAt.Valid {
		return false
	}
	if !event.PriceFromMinor.Valid || event.PriceFromMinor.Int32 < 0 || event.PriceFromMinor.Int32 > c.budget {
		return false
	}
	local := event.StartsAt.Time.In(zone)
	if !c.dates[local.Format(time.DateOnly)] || !c.days[dayType(local.Weekday())] || !c.slots[timeSlot(local.Hour())] {
		return false
	}
	if c.exclusions["nightclubs"] && venue.VenueType == "nightclub" {
		return false
	}
	if c.exclusions["very_loud"] && event.LoudnessLevel.Valid && event.LoudnessLevel.String == "very_loud" {
		return false
	}
	if c.exclusions["outdoor"] && event.Indoor.Valid && !event.Indoor.Bool {
		return false
	}
	if (c.exclusions["far_from_metro"] || hasEnabledRadius(radii)) && !validCoordinates(venue.Latitude, venue.Longitude) {
		return false
	}
	if c.exclusions["far_from_metro"] {
		d, ok := catalog.NearestMetroDistance(venue.Latitude, venue.Longitude, stations)
		if !ok || d > metroLimitM+distanceEpsM {
			return false
		}
	}
	for _, r := range radii {
		if r.enabled && catalog.HaversineMeters(r.lat, r.lng, venue.Latitude, venue.Longitude) > float64(r.meters)+distanceEpsM {
			return false
		}
	}
	return true
}
func hasEnabledRadius(radii []radiusConstraint) bool {
	for _, radius := range radii {
		if radius.enabled {
			return true
		}
	}
	return false
}
func dayType(day time.Weekday) string {
	if day == time.Saturday || day == time.Sunday {
		return "weekend"
	}
	return "weekday"
}
func timeSlot(hour int) string {
	switch {
	case hour >= 6 && hour < 12:
		return "morning"
	case hour >= 12 && hour < 17:
		return "day"
	case hour >= 17 && hour < 22:
		return "evening"
	default:
		return "night"
	}
}
func uuidSet(values []uuid.UUID) map[uuid.UUID]bool {
	r := make(map[uuid.UUID]bool, len(values))
	for _, v := range values {
		r[v] = true
	}
	return r
}

func fingerprint(input contracts.BuildInput, c hardConstraints, timezone string, radii ...radiusConstraint) string {
	type radiusFingerprint struct {
		Meters    int32   `json:"m"`
		Latitude  float64 `json:"a"`
		Longitude float64 `json:"o"`
	}
	type payload struct {
		RankerVersion, Room, City                string
		Timezone                                 string
		Round                                    int16
		Pool                                     int32
		FirstVersion, SecondVersion              int32
		Dates, Days, Slots, Exclusions, Previous []string
		Budget                                   int32
		Radii                                    []radiusFingerprint
	}
	previous := make([]string, 0, len(input.PreviousEventIDs))
	for id := range uuidSet(input.PreviousEventIDs) {
		previous = append(previous, id.String())
	}
	sort.Strings(previous)
	p := payload{
		RankerVersion: rankerVersion, Room: input.RoomID.String(), City: input.CityID.String(), Timezone: timezone,
		Round: input.RoundNo, Pool: input.PoolVersion,
		FirstVersion: input.FirstIntent.Version, SecondVersion: input.SecondIntent.Version,
		Dates: sortedKeys(c.dates), Days: sortedKeys(c.days), Slots: sortedKeys(c.slots),
		Exclusions: sortedKeys(c.exclusions), Previous: previous, Budget: c.budget,
	}
	for _, r := range radii {
		if r.enabled {
			p.Radii = append(p.Radii, radiusFingerprint{r.meters, r.lat, r.lng})
		}
	}
	sort.Slice(p.Radii, func(i, j int) bool {
		if p.Radii[i].Meters != p.Radii[j].Meters {
			return p.Radii[i].Meters < p.Radii[j].Meters
		}
		if p.Radii[i].Latitude != p.Radii[j].Latitude {
			return p.Radii[i].Latitude < p.Radii[j].Latitude
		}
		return p.Radii[i].Longitude < p.Radii[j].Longitude
	})
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func sortedKeys(values map[string]bool) []string {
	r := make([]string, 0, len(values))
	for value := range values {
		r = append(r, value)
	}
	sort.Strings(r)
	return r
}
