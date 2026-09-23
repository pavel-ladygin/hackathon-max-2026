package kudago

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	kudagoSource  = "kudago"
	moscowZone    = "Europe/Moscow"
	defaultStatus = providers.EventStatusPublished
)

var categoryMapping = map[string]string{
	"concert":               "concerts",
	"show":                  "concerts",
	"music":                 "concerts",
	"cinema":                "cinema",
	"movie":                 "cinema",
	"films":                 "cinema",
	"theater":               "theatre",
	"theatre":               "theatre",
	"circus":                "theatre",
	"stand-up":              "standup",
	"standup":               "standup",
	"comedy-club":           "standup",
	"kvn":                   "standup",
	"exhibition":            "exhibitions",
	"permanent-exhibitions": "exhibitions",
	"photo":                 "exhibitions",
	"sport":                 "sports",
	"sports":                "sports",
	"yoga":                  "sports",
	"dance-trainings":       "sports",
	"food":                  "food",
	"gastronomy":            "food",
	"restaurants":           "food",
	"party":                 "parties",
	"night":                 "parties",
	"evening":               "parties",
	"ball":                  "parties",
	"masquerade":            "parties",
	"speed-dating":          "parties",
	"festival":              "festivals",
	"holiday":               "festivals",
	"flashmob":              "festivals",
	"yarmarki":              "festivals",
	"fair":                  "festivals",
	"tour":                  "walks",
	"recreation":            "walks",
	"open":                  "walks",
	"other":                 "other",
}

func normalizeEvents(events []eventDTO, actualSince, actualUntil time.Time) []providers.NormalizedEvent {
	result := make([]providers.NormalizedEvent, 0)
	seen := make(map[string]struct{})
	for _, event := range events {
		for _, occurrence := range normalizeEvent(event, actualSince, actualUntil) {
			key := occurrence.Source + "\x00" + occurrence.ExternalID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, occurrence)
		}
	}
	return result
}

func normalizeEvent(event eventDTO, actualSince, actualUntil time.Time) []providers.NormalizedEvent {
	title := strings.TrimSpace(event.Title)
	if event.ID <= 0 || title == "" || event.Place == nil || strings.TrimSpace(event.Place.Title) == "" || !validCoordinates(event.Place.Coords) {
		return nil
	}

	base := providers.NormalizedEvent{
		Source:      kudagoSource,
		IsDemo:      false,
		Title:       title,
		Subtitle:    subtitle(event),
		Description: description(event),
		Venue:       normalizeVenue(*event.Place),
		Timezone:    moscowZone,
		Currency:    "RUB",
		TicketURL:   providerPageURL(event.SiteURL),
		Status:      defaultStatus,
		AgeRating:   ageRating(string(event.AgeRestriction)),
		PublishedAt: unixPointer(event.PublicationDate),
		Categories:  normalizeCategories(event.Categories),
		Images:      normalizeImages(event.Images),
	}
	// KudaGo exposes only site_url for its event page, with no ticket inventory
	// or registration-open field. For this provider availability therefore means
	// that a validated KudaGo event-page CTA exists, not that stock is confirmed.
	base.TicketAvailable = base.TicketURL != nil
	if event.IsFree {
		zero := int32(0)
		base.PriceFromMinor = &zero
		base.PriceToMinor = &zero
	}

	result := make([]providers.NormalizedEvent, 0, len(event.Dates))
	seen := make(map[int64]struct{}, len(event.Dates))
	for _, date := range event.Dates {
		if date.Start <= 0 {
			continue
		}
		startsAt := time.Unix(date.Start, 0).UTC()
		if startsAt.Before(actualSince) || startsAt.After(actualUntil) {
			continue
		}
		if _, duplicate := seen[date.Start]; duplicate {
			continue
		}
		seen[date.Start] = struct{}{}
		occurrence := base
		occurrence.ExternalID = fmt.Sprintf("%d:%d", event.ID, date.Start)
		occurrence.StartsAt = startsAt
		if date.End != nil && *date.End > date.Start {
			end := time.Unix(*date.End, 0).UTC()
			occurrence.EndsAt = &end
		}
		result = append(result, occurrence)
	}
	return result
}

func normalizeVenue(place placeDTO) providers.NormalizedVenue {
	venue := providers.NormalizedVenue{
		Name:      strings.TrimSpace(place.Title),
		Address:   strings.TrimSpace(place.Address),
		Latitude:  place.Coords.Lat,
		Longitude: place.Coords.Lon,
		VenueType: "other",
	}
	if place.ID > 0 {
		venue.ExternalID = strconv.FormatInt(place.ID, 10)
	}
	if metro := strings.TrimSpace(place.Subway); metro != "" {
		venue.Metro = &metro
	}
	return venue
}

func validCoordinates(coords coordsDTO) bool {
	if coords.Lat == nil || coords.Lon == nil {
		return false
	}
	lat, lon := *coords.Lat, *coords.Lon
	return !math.IsNaN(lat) && !math.IsNaN(lon) && !math.IsInf(lat, 0) && !math.IsInf(lon, 0) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func subtitle(event eventDTO) *string {
	value := strings.TrimSpace(event.Tagline)
	if value == "" {
		value = strings.TrimSpace(event.ShortTitle)
	}
	if value == "" || value == strings.TrimSpace(event.Title) {
		return nil
	}
	return &value
}

func description(event eventDTO) string {
	if value := strings.TrimSpace(event.BodyText); value != "" {
		return value
	}
	return strings.TrimSpace(event.Description)
}

func providerPageURL(raw string) *string {
	value := strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" {
		return nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "kudago.com" && !strings.HasSuffix(host, ".kudago.com") {
		return nil
	}
	return &value
}

func normalizeCategories(source []string) []providers.NormalizedCategory {
	result := make([]providers.NormalizedCategory, 0, len(source))
	seen := make(map[string]struct{}, len(source))
	primary := -1
	for _, raw := range source {
		slug := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(raw)), "_", "-")
		mapped, recognized := categoryMapping[slug]
		if !recognized {
			mapped = "other"
		}
		if _, duplicate := seen[mapped]; duplicate {
			continue
		}
		seen[mapped] = struct{}{}
		result = append(result, providers.NormalizedCategory{Slug: mapped, Weight: 1})
		if recognized && primary == -1 {
			primary = len(result) - 1
		}
	}
	if len(result) == 0 {
		result = append(result, providers.NormalizedCategory{Slug: "other", Weight: 1})
	}
	if primary == -1 {
		primary = 0
	}
	result[primary].IsPrimary = true
	return result
}

func normalizeImages(source []eventImage) []providers.NormalizedImage {
	result := make([]providers.NormalizedImage, 0, len(source))
	seen := make(map[string]struct{}, len(source))
	for _, image := range source {
		value := strings.TrimSpace(image.Image)
		parsed, err := url.ParseRequestURI(value)
		if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		role := "gallery"
		if len(result) == 0 {
			role = "card"
		}
		result = append(result, providers.NormalizedImage{URL: value, Role: role, Position: int32(len(result))})
	}
	return result
}

func ageRating(raw string) *string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	switch value {
	case "0", "6", "12", "16", "18":
		value += "+"
	case "0+", "6+", "12+", "16+", "18+":
	default:
		value = "unknown"
	}
	return &value
}

func unixPointer(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	value := time.Unix(seconds, 0).UTC()
	return &value
}
