package discovery

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
)

var ErrInvalidHome = errors.New("invalid home feed")

// HomeSearcher is the existing discovery application service capability used by
// Home. It deliberately has no room or recommendation-pool dependency.
type HomeSearcher interface {
	Search(context.Context, SearchFilter) (Page, error)
}

// HomeCityReader supplies the profile city only when a request omits city_id.
type HomeCityReader interface {
	UserCity(context.Context, uuid.UUID) (uuid.UUID, error)
}

// HomePreferencesReader provides the user's already persisted preferences.
type HomePreferencesReader interface {
	Get(context.Context, uuid.UUID) (preferences.Value, bool, error)
}

// HomeActiveRoomReader supplies the caller's resumable room without exposing
// room persistence details to the home-feed service.
type HomeActiveRoomReader interface {
	GetActiveRoom(context.Context, uuid.UUID) (ActiveRoom, bool, error)
}

type HomeRoomCloseNoticeReader interface {
	GetRoomCloseNotice(context.Context, uuid.UUID) (*RoomClosedNotice, error)
}

// ActiveRoom is the minimal public projection needed by the home screen.
type ActiveRoom struct {
	ID     uuid.UUID
	Name   string
	CityID uuid.UUID
	State  string
}

type RoomClosedNotice struct {
	RoomID              uuid.UUID
	RoomName            string
	ClosedByID          uuid.UUID
	ClosedByDisplayName string
	ClosedAt            time.Time
}

// HomeInput is the normalized, request-only home-feed input.
type HomeInput struct {
	UserID   uuid.UUID
	CityID   *uuid.UUID
	Location *Location
	Limit    int
}

type HomeSection struct {
	Type  string
	Title string
	Items []Card
}

type HomeFeed struct {
	ID               uuid.UUID
	GeneratedAt      time.Time
	ActiveRoom       *ActiveRoom
	RoomClosedNotice *RoomClosedNotice
	Sections         []HomeSection
}

// HomeService builds a bounded deterministic set of display sections from the
// established discovery service and permanent preference capability.
type HomeService struct {
	searcher    HomeSearcher
	cities      HomeCityReader
	preferences HomePreferencesReader
	rooms       HomeActiveRoomReader
	now         func() time.Time
	newID       func() uuid.UUID
}

func NewHomeService(searcher HomeSearcher, cities HomeCityReader, preferencesReader HomePreferencesReader, rooms HomeActiveRoomReader) *HomeService {
	return &HomeService{searcher: searcher, cities: cities, preferences: preferencesReader, rooms: rooms, now: time.Now, newID: uuid.New}
}

func (s *HomeService) Home(ctx context.Context, input HomeInput) (HomeFeed, error) {
	if s.searcher == nil || s.cities == nil || s.preferences == nil || s.rooms == nil || input.UserID == uuid.Nil || input.Limit < 0 || input.Limit > maxLimit || input.Location != nil && !validLocation(*input.Location) {
		return HomeFeed{}, ErrInvalidHome
	}
	if input.Limit == 0 {
		input.Limit = defaultLimit
	}

	cityID, err := s.city(ctx, input)
	if err != nil {
		return HomeFeed{}, err
	}
	preference, hasPreferences, err := s.preferences.Get(ctx, input.UserID)
	if err != nil {
		return HomeFeed{}, err
	}
	activeRoom, hasActiveRoom, err := s.rooms.GetActiveRoom(ctx, input.UserID)
	if err != nil {
		return HomeFeed{}, err
	}

	base := SearchFilter{UserID: input.UserID, CityID: cityID, Limit: input.Limit}
	sections := make([]HomeSection, 0, 4)
	if page, err := s.searcher.Search(ctx, withLimit(base, 1)); err != nil {
		return HomeFeed{}, err
	} else if len(page.Items) != 0 {
		sections = append(sections, HomeSection{Type: "hero", Title: "Рекомендуем", Items: page.Items})
	}
	if page, err := s.searcher.Search(ctx, base); err != nil {
		return HomeFeed{}, err
	} else if len(page.Items) != 0 {
		sections = append(sections, HomeSection{Type: "popular", Title: "Популярное", Items: page.Items})
	}
	if hasPreferences {
		forYou := base
		forYou.CategorySlugs = append([]string(nil), preference.InterestSlugs...)
		forYou.DayTypes = append([]string(nil), preference.UsualDayTypes...)
		forYou.TimeSlots = append([]string(nil), preference.UsualTimeSlots...)
		budget := int32(preference.BudgetMaxMinor)
		forYou.PriceMaxMinor = &budget
		if page, err := s.searcher.Search(ctx, forYou); err != nil {
			return HomeFeed{}, err
		} else if len(page.Items) != 0 {
			sections = append(sections, HomeSection{Type: "for_you", Title: "Для вас", Items: page.Items})
		}
	}
	if input.Location != nil {
		nearby := base
		nearby.Location = input.Location
		nearby.Limit = maxLimit
		page, err := s.searcher.Search(ctx, nearby)
		if err != nil {
			return HomeFeed{}, err
		}
		items := nearest(page.Items, input.Limit)
		if len(items) != 0 {
			sections = append(sections, HomeSection{Type: "nearby", Title: "Рядом с вами", Items: items})
		}
	}
	feed := HomeFeed{ID: s.newID(), GeneratedAt: s.now().UTC(), Sections: sections}
	if hasActiveRoom {
		feed.ActiveRoom = &activeRoom
	}
	if reader, ok := s.rooms.(HomeRoomCloseNoticeReader); ok {
		feed.RoomClosedNotice, err = reader.GetRoomCloseNotice(ctx, input.UserID)
		if err != nil {
			return HomeFeed{}, err
		}
	}
	return feed, nil
}

func (s *HomeService) city(ctx context.Context, input HomeInput) (uuid.UUID, error) {
	if input.CityID != nil {
		if *input.CityID == uuid.Nil {
			return uuid.Nil, ErrInvalidHome
		}
		return *input.CityID, nil
	}
	cityID, err := s.cities.UserCity(ctx, input.UserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cityID == uuid.Nil) {
		return uuid.Nil, ErrInvalidHome
	}
	return cityID, err
}

func withLimit(filter SearchFilter, limit int) SearchFilter { filter.Limit = limit; return filter }

func nearest(cards []Card, limit int) []Card {
	items := append([]Card(nil), cards...)
	sort.SliceStable(items, func(i, j int) bool {
		left, right := distanceKey(items[i]), distanceKey(items[j])
		if left != right {
			return left < right
		}
		if !items[i].StartsAt.Equal(items[j].StartsAt) {
			return items[i].StartsAt.Before(items[j].StartsAt)
		}
		return items[i].ID.String() < items[j].ID.String()
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func distanceKey(card Card) int {
	if card.DistanceMeters == nil {
		return int(^uint(0) >> 1)
	}
	return *card.DistanceMeters
}
