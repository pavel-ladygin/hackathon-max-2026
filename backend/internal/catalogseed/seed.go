// Package catalogseed installs the explicit demo catalog, separately from migrations.
package catalogseed

import (
	"context"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Keep Moscow date handling available in minimal runtime images.

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const MoscowCityID = "a0f625ee-2154-5a45-8afe-37adf955ec24"
const timezone = "Europe/Moscow"

var namespace = uuid.MustParse("6aeaf741-169b-5900-88e4-7b073e9cbfd2")

// EffectiveBaseDate resolves the date once per run. Explicit dates are never
// shifted to today, so replaying an old fixture remains reproducible.
func EffectiveBaseDate(value string, now time.Time) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load demo timezone: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = now.In(location).Format(time.DateOnly)
	}
	date, err := time.ParseInLocation(time.DateOnly, value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("DEMO_BASE_DATE must be YYYY-MM-DD")
	}
	return date, nil
}

type Counts struct{ Cities, MetroStations, Venues, Categories, Events, Images int }

// Apply atomically reconciles only the named demo fixtures. IDs never include
// dates, titles or the current time. A later base date updates the same events.
func Apply(ctx context.Context, db *store.Pool, base time.Time) (Counts, error) {
	if base.IsZero() {
		return Counts{}, fmt.Errorf("demo base date is required")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Counts{}, err
	}
	base, err = EffectiveBaseDate(base.In(location).Format(time.DateOnly), base)
	if err != nil {
		return Counts{}, err
	}
	data := fixtures(base)
	err = db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		if err := q.LockDemoSeed(ctx); err != nil {
			return err
		}
		if err := q.SeedCity(ctx, data.city); err != nil {
			return err
		}
		for _, station := range data.metro {
			if err := q.SeedMetroStation(ctx, station); err != nil {
				return err
			}
		}
		for _, venue := range data.venues {
			if err := q.SeedVenue(ctx, venue); err != nil {
				return err
			}
		}
		for _, event := range data.events {
			changed, err := q.SeedEvent(ctx, event)
			if err != nil {
				return err
			}
			if changed != 1 {
				return fmt.Errorf("demo identity conflicts with an existing non-demo event")
			}
			if err := q.ClearDemoCategories(ctx, event.ID); err != nil {
				return err
			}
			if err := q.ClearDemoImages(ctx, event.ID); err != nil {
				return err
			}
		}
		for _, category := range data.categories {
			if err := q.SeedCategory(ctx, category); err != nil {
				return err
			}
		}
		for _, image := range data.images {
			if err := q.SeedImage(ctx, image); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Counts{}, fmt.Errorf("apply demo catalog: %w", err)
	}
	return Counts{1, len(data.metro), len(data.venues), len(categorySlugs), len(data.events), len(data.images)}, nil
}

func stableID(kind, key string) uuid.UUID { return uuid.NewSHA1(namespace, []byte(kind+"/"+key)) }
func text(value string) pgtype.Text       { return pgtype.Text{String: value, Valid: true} }
func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
func integer(value int32) pgtype.Int4 { return pgtype.Int4{Int32: value, Valid: true} }

var categorySlugs = [...]string{"concerts", "cinema", "theatre", "standup", "exhibitions", "sports", "food", "parties", "festivals", "walks", "other"}

type dataset struct {
	city       platform.SeedCityParams
	metro      []platform.SeedMetroStationParams
	venues     []platform.SeedVenueParams
	events     []platform.SeedEventParams
	categories []platform.SeedCategoryParams
	images     []platform.SeedImageParams
}

// The locations are illustrative Moscow demo venues, not provider listings.
// Immutable keys survive editorial changes to display names and addresses.
var locations = [...]struct {
	key, name, address, metro, district, kind string
	lat, lng                                  float64
}{
	{"river-stage", "Демо: сцена у реки", "Космодамианская набережная, 52", "Павелецкая", "Замоскворечье", "concert_hall", 55.7334, 37.6467},
	{"film-club", "Демо: киноклуб", "Новый Арбат, 24", "Арбатская", "Арбат", "cinema", 55.7520, 37.5870},
	{"small-theatre", "Демо: малый театр", "Садовая-Каретная улица, 3", "Чеховская", "Тверской", "theatre", 55.7705, 37.6090},
	{"comedy-cafe", "Демо: кафе историй", "Покровка, 17", "Китай-город", "Басманный", "cafe", 55.7590, 37.6465},
	{"gallery", "Демо: городская галерея", "Крымский Вал, 10", "Октябрьская", "Якиманка", "museum", 55.7351, 37.6057},
	{"sports-arena", "Демо: спортивная арена", "Лужнецкая набережная, 24", "Спортивная", "Хамовники", "stadium", 55.7158, 37.5537},
	{"food-lab", "Демо: гастролаборатория", "Пятницкая улица, 20", "Новокузнецкая", "Замоскворечье", "restaurant", 55.7414, 37.6290},
	{"night-club", "Демо: ночная сцена", "Большая Дмитровка, 13", "Театральная", "Тверской", "nightclub", 55.7623, 37.6134},
	{"park-stage", "Демо: сцена в парке", "Улица Крымский Вал, 9", "Парк культуры", "Якиманка", "outdoor", 55.7298, 37.6000},
	{"forest-walk", "Демо: лесная прогулка", "Лосиный остров, лесная тропа", "Сокольники", "Метрогородок", "outdoor", 55.8500, 37.8000},
	{"workshop", "Демо: творческая мастерская", "Бауманская улица, 15", "Бауманская", "Басманный", "other", 55.7730, 37.6790},
	{"quiet-cafe", "Демо: тихое кафе", "Чистопрудный бульвар, 12", "Чистые пруды", "Басманный", "cafe", 55.7610, 37.6385},
}

var stations = [...]struct {
	key, name string
	lat, lng  float64
}{
	{"paveletskaya", "Павелецкая", 55.7313, 37.6361}, {"arbatskaya", "Арбатская", 55.7522, 37.6061},
	{"chekhovskaya", "Чеховская", 55.7659, 37.6085}, {"kitay-gorod", "Китай-город", 55.7565, 37.6333},
	{"oktyabrskaya", "Октябрьская", 55.7293, 37.6110}, {"sportivnaya", "Спортивная", 55.7233, 37.5639},
	{"novokuznetskaya", "Новокузнецкая", 55.7415, 37.6295}, {"teatralnaya", "Театральная", 55.7580, 37.6171},
	{"park-kultury", "Парк культуры", 55.7352, 37.5931}, {"sokolniki", "Сокольники", 55.7893, 37.6797},
	{"baumanskaya", "Бауманская", 55.7724, 37.6806}, {"chistye-prudy", "Чистые пруды", 55.7648, 37.6383},
}

func fixtures(base time.Time) dataset {
	cityID := uuid.MustParse(MoscowCityID)
	d := dataset{city: platform.SeedCityParams{ID: cityID, Name: "Москва", Timezone: timezone, CenterLat: 55.7558, CenterLng: 37.6173}}
	for _, s := range stations {
		d.metro = append(d.metro, platform.SeedMetroStationParams{ID: stableID("metro", s.key), CityID: cityID, Name: s.name, Latitude: s.lat, Longitude: s.lng})
	}
	for _, v := range locations {
		d.venues = append(d.venues, platform.SeedVenueParams{ID: stableID("venue", v.key), CityID: cityID, Name: v.name, Address: v.address, Latitude: v.lat, Longitude: v.lng, Metro: text(v.metro), District: text(v.district), VenueType: v.kind})
	}
	titles := [...]string{"Джазовый вечер", "Кино и обсуждение", "Камерный спектакль", "Вечер стендапа", "Цвет города", "Спортивный день", "Вкусы Москвы", "Ночной ритм", "Городской фестиваль", "Прогулка по тропам", "Творческая встреча"}
	hours := [...]int{10, 14, 19, 23}
	loudness := [...]string{"quiet", "normal", "loud", "very_loud"}
	for variant := 0; variant < 4; variant++ {
		for category, slug := range categorySlugs {
			key := fmt.Sprintf("moscow-%s-%02d", slug, variant+1)
			id := stableID("event", key)
			venue := category
			if category == 10 && variant%2 == 1 {
				venue = 11
			}
			start := base.AddDate(0, 0, 1+category+variant*3).Add(time.Duration(hours[variant]) * time.Hour)
			price := integer(int32(50000 + category*15000))
			if variant == 0 {
				price = integer(0)
			}
			if variant == 1 {
				price = pgtype.Int4{}
			}
			priceTo := price
			if price.Valid && price.Int32 > 0 {
				priceTo = integer(price.Int32 + 30000)
			}
			status := "published"
			if variant == 3 && category == 0 {
				status = "sold_out"
			}
			if variant == 3 && category == 1 {
				status = "cancelled"
			}
			// Reserved .invalid URLs represent demo ticket states, never real sales.
			available := status == "published" && variant != 1
			var ticket pgtype.Text
			if variant != 1 {
				ticket = text("https://tickets.example.invalid/demo/" + key)
			}
			d.events = append(d.events, platform.SeedEventParams{
				ID: id, Source: "demo", ExternalID: key, SourceUpdatedAt: timestamp(base), IsDemo: true,
				Title: fmt.Sprintf("%s — демо %d", titles[category], variant+1), Subtitle: text("Подготовленное демо-событие"),
				Description: "Демонстрационная программа MAX Вместе. Событие, цена и наличие билетов вымышлены; покупка недоступна.",
				VenueID:     d.venues[venue].ID, StartsAt: timestamp(start), EndsAt: timestamp(start.Add(2 * time.Hour)), Timezone: timezone,
				PriceFromMinor: price, PriceToMinor: priceTo, Currency: "RUB", TicketUrl: ticket, TicketAvailable: available, Status: status,
				AgeRating: text("18+"), Indoor: pgtype.Bool{Bool: locations[venue].kind != "outdoor", Valid: true}, LoudnessLevel: text(loudness[variant]),
				PublishedAt: timestamp(base), UpdatedAt: timestamp(base),
			})
			d.categories = append(d.categories, platform.SeedCategoryParams{EventID: id, CategorySlug: slug, Weight: 1, IsPrimary: true})
			if variant == 2 {
				d.categories = append(d.categories, platform.SeedCategoryParams{EventID: id, CategorySlug: categorySlugs[(category+1)%len(categorySlugs)], Weight: 0.5, IsPrimary: false})
			}
			// A fixed asset avoids randomly changing images on identical seed runs.
			d.images = append(d.images, platform.SeedImageParams{ID: stableID("image", key+"/card"), EventID: id, Url: "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?auto=format&fit=crop&w=1200&h=800&q=80", Width: integer(1200), Height: integer(800), Role: "card", Position: 0})
		}
	}
	return d
}
