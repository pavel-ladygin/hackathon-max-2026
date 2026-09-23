package recommendations

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalog"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const (
	featureCategoryAffinity         = "category_affinity"
	featureCurrentIntentCategoryFit = "current_intent_category_fit"
	featureTimeQuality              = "time_quality"
	featureBudgetHeadroom           = "budget_headroom"
	featureDistanceQuality          = "distance_quality"
	featureNovelty                  = "novelty"
	featurePopularity               = "popularity"
)

type participantFeatures struct {
	categoryAffinity, currentIntentCategoryFit float64
	behavioralAffinity                         float64
	timeQuality, budgetHeadroom                float64
	distanceQuality, novelty, popularity       float64
}

type rankedEvent struct {
	event        catalog.Event
	venue        platform.Venue
	primary      string
	groupScore   float64
	tieBreak     []byte
	score        contracts.ScoreSnapshot
	features     contracts.FeatureSnapshot
	explanations []contracts.Explanation
}

func scoreEvent(event catalog.Event, venue platform.Venue, first, second normalizedIntent) rankedEvent {
	firstFeatures := featuresFor(event, venue, first)
	secondFeatures := featuresFor(event, venue, second)
	firstScore := userScore(firstFeatures)
	secondScore := userScore(secondFeatures)
	minimum, mean := firstScore, (firstScore+secondScore)/2
	if secondScore < minimum {
		minimum = secondScore
	}
	group := .65*minimum + .35*mean
	features := contracts.FeatureSnapshot{
		featureCategoryAffinity:         (firstFeatures.categoryAffinity + secondFeatures.categoryAffinity) / 2,
		featureCurrentIntentCategoryFit: (firstFeatures.currentIntentCategoryFit + secondFeatures.currentIntentCategoryFit) / 2,
		featureTimeQuality:              (firstFeatures.timeQuality + secondFeatures.timeQuality) / 2,
		featureBudgetHeadroom:           (firstFeatures.budgetHeadroom + secondFeatures.budgetHeadroom) / 2,
		featureDistanceQuality:          (firstFeatures.distanceQuality + secondFeatures.distanceQuality) / 2,
		featureNovelty:                  (firstFeatures.novelty + secondFeatures.novelty) / 2,
		featurePopularity:               (firstFeatures.popularity + secondFeatures.popularity) / 2,
	}
	return rankedEvent{
		event:      event,
		venue:      venue,
		primary:    primaryCategory(event),
		groupScore: group,
		score: contracts.ScoreSnapshot{
			GroupScore:           float32(group),
			ParticipantScoreMin:  float32(minimum),
			ParticipantScoreMean: float32(mean),
		},
		features:     features,
		explanations: explanations(event, first, second, firstFeatures, secondFeatures),
	}
}

func featuresFor(event catalog.Event, venue platform.Venue, intent normalizedIntent) participantFeatures {
	budget := float64(intent.budget)
	headroom := 0.0

	if event.PriceFromMinor.Valid {
		price := float64(event.PriceFromMinor.Int32)

		if budget == 0 && price == 0 {
			headroom = 1
		} else if budget > 0 {
			headroom = clamp01(1 - price/budget)
		}

		if intent.profile.present && intent.profile.budget > 0 && price <= float64(intent.profile.budget) {
			profileHeadroom := .5 * clamp01(1-price/float64(intent.profile.budget))
			headroom = math.Max(headroom, profileHeadroom)
		}
	}
	distance := 0.0
	if intent.radius.enabled {
		latitude, longitude, ok := venueCoordinates(venue)
		if !ok {
			return participantFeatures{}
		}
		d := catalog.HaversineMeters(intent.radius.lat, intent.radius.lng, latitude, longitude)
		if intent.radius.meters == 0 {
			if d == 0 {
				distance = 1
			}
		} else {
			distance = clamp01(1 - d/float64(intent.radius.meters))
		}
	}
	timeQuality := boolFloat(len(intent.days) > 0 || len(intent.slots) > 0)
	if timeQuality == 0 && profileTimeFit(event, intent.profile) {
		timeQuality = .5
	}
	return participantFeatures{
		categoryAffinity:         .5 * categoryFit(event, intent.profile.categories),
		currentIntentCategoryFit: categoryFit(event, intent.categories),
		behavioralAffinity:       behavioralCategoryFit(event, intent.behavior.categories),
		timeQuality:              timeQuality,
		budgetHeadroom:           headroom,
		distanceQuality:          distance,
		novelty:                  1,
	}
}

func profileTimeFit(event catalog.Event, profile normalizedProfile) bool {
	if !profile.present || (len(profile.days) == 0 && len(profile.slots) == 0) || !event.StartsAt.Valid {
		return false
	}
	location, err := time.LoadLocation(event.Timezone)
	if err != nil {
		location = time.UTC
	}
	local := event.StartsAt.Time.In(location)
	return (len(profile.days) == 0 || profile.days[dayType(local.Weekday())]) &&
		(len(profile.slots) == 0 || profile.slots[timeSlot(local.Hour())])
}

func userScore(f participantFeatures) float64 {
	return .30*f.categoryAffinity + .20*f.currentIntentCategoryFit + .15*f.timeQuality +
		.15*f.budgetHeadroom + .10*f.distanceQuality + .05*f.novelty + .05*f.popularity +
		.06*f.behavioralAffinity
}

func behavioralCategoryFit(event catalog.Event, categories map[string]float64) float64 {
	if len(categories) == 0 {
		return 0
	}
	primary := primaryCategory(event)
	return categories[primary]
}

func categoryFit(event catalog.Event, categories map[string]bool) float64 {
	best := 0.0
	for _, category := range event.Categories {
		if categories[normalizeCategorySlug(category.CategorySlug)] {
			best = math.Max(best, clamp01(float64(category.Weight)))
		}
	}
	return best
}

func hasCategory(event catalog.Event, categories map[string]bool) bool {
	for _, category := range event.Categories {
		if categories[normalizeCategorySlug(category.CategorySlug)] {
			return true
		}
	}
	return false
}

func primaryCategory(event catalog.Event) string {
	primary := ""
	for _, category := range event.Categories {
		if !category.IsPrimary {
			continue
		}
		slug := normalizeCategorySlug(category.CategorySlug)
		if slug != "" && (primary == "" || slug < primary) {
			primary = slug
		}
	}
	return primary
}

func explanations(event catalog.Event, first, second normalizedIntent, a, b participantFeatures) []contracts.Explanation {
	result := make([]contracts.Explanation, 0, 3)
	if hasCategory(event, first.categories) && hasCategory(event, second.categories) {
		result = append(result, contracts.Explanation{Code: "shared_category", Text: "Подходит по интересам"})
	}
	if (a.categoryAffinity+b.categoryAffinity)/2 > 0 && len(result) < 3 {
		result = append(result, contracts.Explanation{Code: "profile_affinity", Text: "Учитывает постоянные предпочтения"})
	}
	if a.timeQuality > 0 && b.timeQuality > 0 {
		result = append(result, contracts.Explanation{Code: "time_fit", Text: "Подходит по времени"})
	}
	if (a.budgetHeadroom+b.budgetHeadroom)/2 >= .25 {
		result = append(result, contracts.Explanation{Code: "budget_fit", Text: "Подходит по стоимости"})
	}
	if a.distanceQuality > 0 && b.distanceQuality > 0 && len(result) < 3 {
		result = append(result, contracts.Explanation{Code: "nearby", Text: "Удобное расположение"})
	}
	if len(result) > 3 {
		return result[:3]
	}
	return result
}

func tieBreak(secret []byte, roomID uuid.UUID, poolVersion int32, eventID uuid.UUID) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(fmt.Sprintf("%s|%d|%s", roomID, poolVersion, eventID)))
	return mac.Sum(nil)
}

func bytesCompare(a, b []byte) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Max(0, math.Min(1, value))
}
func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func normalizeCategorySlug(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func normalizeCategories(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if slug := normalizeCategorySlug(value); slug != "" {
			result[slug] = true
		}
	}
	return result
}
