package timepad

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	timepadSource = "timepad"
	moscowZone    = "Europe/Moscow"
)

func normalizeEvent(event eventDTO) (providers.NormalizedEvent, bool) {
	normalized, reason := normalizeEventWithReason(event)
	return normalized, reason == ""
}

func normalizeEventWithReason(event eventDTO) (providers.NormalizedEvent, providers.RejectionReason) {
	title := providers.CleanText(event.Name)
	if event.ID <= 0 {
		return providers.NormalizedEvent{}, providers.RejectionInvalidID
	}
	if title == "" {
		return providers.NormalizedEvent{}, providers.RejectionMissingTitle
	}
	if strings.TrimSpace(event.StartsAt) == "" {
		return providers.NormalizedEvent{}, providers.RejectionMissingStartsAt
	}
	startsAt, err := parseTime(event.StartsAt)
	if err != nil {
		return providers.NormalizedEvent{}, providers.RejectionInvalidStartsAt
	}
	if event.Categories.Malformed {
		return providers.NormalizedEvent{}, providers.RejectionMalformedCategories
	}
	registrationURL := ticketURL(event.URL)
	status, providerActive := normalizeLifecycle(event)
	venueName := providers.CleanText(event.Location.Address)
	if venueName == "" {
		venueName = providers.CleanText(event.Organization.Name)
	}
	if venueName == "" {
		venueName = "Москва"
	}

	normalized := providers.NormalizedEvent{
		Source:     timepadSource,
		ExternalID: strconv.FormatInt(event.ID, 10),
		Title:      title,
		// Timepad's description_short is currently the only requested text
		// source. Subtitle and Description are therefore aliases, not independent
		// provider fields.
		Subtitle:    providers.CleanOptionalText(event.DescriptionShort),
		Description: providers.CleanText(event.DescriptionShort),
		Venue: providers.NormalizedVenue{
			ExternalID: fmt.Sprintf("event:%d", event.ID),
			Name:       venueName,
			Address:    providers.CleanText(event.Location.Address),
			VenueType:  "other",
		},
		StartsAt:        startsAt,
		Timezone:        moscowZone,
		Currency:        "RUB",
		TicketURL:       registrationURL,
		TicketAvailable: registrationURL != nil && event.RegistrationData.IsRegistrationOpen,
		Status:          status,
		ProviderActive:  providerActive,
		AgeRating:       ageRating(string(event.AgeLimit)),
		Categories:      normalizeCategories(event.Categories.Values),
		Images:          normalizeImages(event.PosterImage),
	}
	if len(event.Location.Coordinates) >= 2 && validCoordinates(event.Location.Coordinates[0], event.Location.Coordinates[1]) {
		normalized.Venue.Latitude = &event.Location.Coordinates[0]
		normalized.Venue.Longitude = &event.Location.Coordinates[1]
	}
	if endsAt, err := parseTime(event.EndsAt); err == nil && endsAt.After(startsAt) {
		normalized.EndsAt = &endsAt
	}
	if createdAt, err := parseTime(event.CreatedAt); err == nil {
		normalized.PublishedAt = &createdAt
	}
	normalized.PriceFromMinor, normalized.PriceToMinor = normalizePrices(event)
	return normalized, ""
}

func normalizeLifecycle(event eventDTO) (status string, providerActive bool) {
	accessStatus := strings.ToLower(strings.TrimSpace(event.AccessStatus))
	moderationStatus := strings.ToLower(strings.TrimSpace(event.ModerationStatus))

	// Neither a closed registration nor a non-public visibility state proves
	// that the event was sold out or cancelled. Timepad does not expose an
	// unambiguous lifecycle signal in these fields, so the lifecycle remains
	// published while visibility controls current product eligibility.
	accessActive := accessStatus == "" || accessStatus == "public"
	moderationActive := moderationStatus == "" || moderationStatus == "shown" || moderationStatus == "featured"
	return providers.EventStatusPublished, accessActive && moderationActive
}

func parseTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timepad date")
}

func validCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0) && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}

func optionalString(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	return &value
}

func ticketURL(raw string) *string {
	value := strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" {
		return nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "timepad.ru" && !strings.HasSuffix(host, ".timepad.ru") {
		return nil
	}
	return &value
}

func normalizePrices(event eventDTO) (*int32, *int32) {
	minimum := event.RegistrationData.PriceMin
	maximum := event.RegistrationData.PriceMax
	if minimum == 0 && maximum == 0 {
		found := false
		for _, ticket := range event.TicketTypes {
			if !ticket.IsActive || ticket.Price < 0 || math.IsNaN(ticket.Price) || math.IsInf(ticket.Price, 0) {
				continue
			}
			if !found || ticket.Price < minimum {
				minimum = ticket.Price
			}
			if !found || ticket.Price > maximum {
				maximum = ticket.Price
			}
			found = true
		}
		if !found {
			return nil, nil
		}
	}
	minMinor, minOK := rublesToMinor(minimum)
	maxMinor, maxOK := rublesToMinor(maximum)
	if !minOK || !maxOK || maxMinor < minMinor {
		return nil, nil
	}
	return &minMinor, &maxMinor
}

func rublesToMinor(value float64) (int32, bool) {
	minor := math.Round(value * 100)
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || minor > math.MaxInt32 {
		return 0, false
	}
	return int32(minor), true
}

func normalizeCategories(categories []categoryDTO) []providers.NormalizedCategory {
	result := make([]providers.NormalizedCategory, 0, len(categories))
	seen := make(map[string]struct{})
	for _, category := range categories {
		slug := mapCategory(category.Name)
		if _, duplicate := seen[slug]; duplicate {
			continue
		}
		seen[slug] = struct{}{}
		result = append(result, providers.NormalizedCategory{Slug: slug, Weight: 1, IsPrimary: len(result) == 0})
	}
	if len(result) == 0 {
		result = append(result, providers.NormalizedCategory{Slug: "other", Weight: 1, IsPrimary: true})
	}
	return result
}

func mapCategory(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	for _, mapping := range []struct {
		needles []string
		slug    string
	}{
		{[]string{"концерт", "музык"}, "concerts"},
		{[]string{"кино"}, "cinema"},
		{[]string{"театр", "спектак"}, "theatre"},
		{[]string{"стендап", "юмор", "комеди"}, "standup"},
		{[]string{"выстав", "искусств", "культур"}, "exhibitions"},
		{[]string{"спорт", "фитнес"}, "sports"},
		{[]string{"еда", "гастроном", "кулинар"}, "food"},
		{[]string{"вечерин", "развлеч"}, "parties"},
		{[]string{"фестивал", "праздник"}, "festivals"},
		{[]string{"экскурс", "прогул", "путешеств"}, "walks"},
	} {
		for _, needle := range mapping.needles {
			if strings.Contains(value, needle) {
				return mapping.slug
			}
		}
	}
	return "other"
}

func normalizeImages(image imageDTO) []providers.NormalizedImage {
	value := strings.TrimSpace(image.DefaultURL)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
		return nil
	}
	value = repairMalformedPosterURL(parsed)
	return []providers.NormalizedImage{{URL: value, Role: "card", Position: 0}}
}

var uploadcareUUIDPattern = regexp.MustCompile(`(?i)^/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:/|$)`)

// repairMalformedPosterURL points Timepad Uploadcare URLs at the stored
// original. Timepad often supplies small generated previews, which become
// visibly pixelated when used as large event banners.
func repairMalformedPosterURL(parsed *url.URL) string {
	if !strings.EqualFold(parsed.Hostname(), "ucare.timepad.ru") {
		return parsed.String()
	}

	matches := uploadcareUUIDPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(matches) != 2 {
		return parsed.String()
	}
	return "https://ucare.timepad.ru/" + matches[1] + "/"
}

func ageRating(raw string) *string {
	value := strings.TrimSpace(raw)
	value = strings.TrimRightFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '+' })
	switch value {
	case "0", "6", "12", "16", "18":
		value += "+"
	case "":
		return nil
	default:
		value = "unknown"
	}
	return &value
}
