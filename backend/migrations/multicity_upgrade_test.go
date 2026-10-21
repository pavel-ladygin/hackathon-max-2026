package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/testdbguard"
	"github.com/pressly/goose/v3"
)

// These tests intentionally create uniquely named databases below the dedicated
// disposable cluster. They never run unless the guarded fixture URL is supplied.
// The fixture URL is validated before the first connection or CREATE DATABASE.
func TestMulticityMigrationUpgradeMatrix(t *testing.T) {
	fixtureDSN := strings.TrimSpace(os.Getenv("MULTICITY_TEST_DATABASE_URL"))
	if fixtureDSN == "" {
		t.Skip("MULTICITY_TEST_DATABASE_URL is not set")
	}
	fixture, err := testdbguard.Parse(fixtureDSN, "multicity_fixture")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("M01 fresh Up reaches current under C locale and UTF8 encoding", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, true)
		upAll(t, db)
		assertCurrent(t, pool, true)
		var collate, ctype, encoding string
		if err := db.QueryRow("SELECT datcollate FROM pg_database WHERE datname=current_database()").Scan(&collate); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT datctype FROM pg_database WHERE datname=current_database()").Scan(&ctype); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SHOW server_encoding").Scan(&encoding); err != nil {
			t.Fatal(err)
		}
		if collate != "C" || ctype != "C" || encoding != "UTF8" {
			t.Fatalf("database locale/encoding = %q/%q/%q, want C/C/UTF8", collate, ctype, encoding)
		}
	})

	t.Run("M02/M04 upgrade from 30 applies only 31 and preserves related data", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		before := seedVersion30References(t, db)
		assertCurrent(t, pool, false)
		upAll(t, db)
		assertOnlyVersionAppliedAfter30(t, db)
		assertVersion30References(t, db, before)
		assertCurrent(t, pool, true)
	})

	t.Run("M03 repeated Up is idempotent", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upAll(t, db)
		upAll(t, db)
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("Kazan rows after repeat Up = %d, want 1", count)
		}
		assertCurrent(t, pool, true)
	})

	t.Run("M05 existing canonical metadata is accepted", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ('3d3c83de-691b-510e-85e5-79685c4012ee','Казань','Europe/Moscow',55.78874,49.12214)`)
		if err != nil {
			t.Fatal(err)
		}
		upAll(t, db)
	})

	t.Run("M06 semantic duplicate with another UUID is rejected", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ($1,'Казань','Europe/Moscow',55.78874,49.12214)`, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
	})

	t.Run("M07 conflicting Latin city name is rejected", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ($1,'Kazan','Europe/Moscow',55.78874,49.12214)`, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
	})

	t.Run("M07 reserved UUID with wrong timezone rolls back", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ('3d3c83de-691b-510e-85e5-79685c4012ee','Казань','UTC',55.78874,49.12214)`)
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
		var timezone string
		if err := db.QueryRow(`SELECT timezone FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`).Scan(&timezone); err != nil || timezone != "UTC" {
			t.Fatalf("failed migration changed conflicting city: timezone=%q err=%v", timezone, err)
		}
	})

	t.Run("M07 reserved UUID with wrong coordinates rolls back", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ('3d3c83de-691b-510e-85e5-79685c4012ee','Казань','Europe/Moscow',1,2)`)
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
		var lat, lng float64
		if err := db.QueryRow(`SELECT center_lat, center_lng FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`).Scan(&lat, &lng); err != nil || lat != 1 || lng != 2 {
			t.Fatalf("failed migration changed conflicting coordinates: %v/%v err=%v", lat, lng, err)
		}
	})

	t.Run("M07 reserved UUID with wrong name rolls back", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ('3d3c83de-691b-510e-85e5-79685c4012ee','Other city','Europe/Moscow',55.78874,49.12214)`)
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
		var name string
		if err := db.QueryRow(`SELECT name FROM cities WHERE id='3d3c83de-691b-510e-85e5-79685c4012ee'`).Scan(&name); err != nil || name != "Other city" {
			t.Fatalf("failed migration changed conflicting city name: %q err=%v", name, err)
		}
	})

	t.Run("M08 C locale detects uppercase Cyrillic semantic conflict", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, true)
		upTo(t, db, 30)
		_, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng)
			VALUES ($1,'КАЗАНЬ','Europe/Moscow',55.78874,49.12214)`, uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		mustFailUp31(t, db)
	})

	t.Run("M09 Down 31 preserves referenced Kazan metadata", func(t *testing.T) {
		db, _ := newMulticityMigrationDatabase(t, fixture, false)
		upAll(t, db)
		kazan := uuid.MustParse("3d3c83de-691b-510e-85e5-79685c4012ee")
		before := seedVersion30References(t, db)
		user := insertUser(t, db, kazan)
		if _, err := db.Exec(`INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type)
			VALUES($1,$2,'Referenced Kazan venue','fixture',55.78874,49.12214,'other')`, uuid.New(), kazan); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO rooms(id,creator_user_id,city_id,name,state,expires_at,public_plan)
			VALUES($1,$2,$3,'Referenced Kazan room','collecting_intents',now()+interval '1 day','{}')`, uuid.New(), user, kazan); err != nil {
			t.Fatal(err)
		}
		configureGoose(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := goose.DownToContext(ctx, db, ".", 30); err != nil {
			t.Fatalf("Down to 30: %v", err)
		}
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM cities WHERE id=$1`, kazan).Scan(&count); err != nil || count != 1 {
			t.Fatalf("Down 31 removed referenced Kazan metadata: count=%d err=%v", count, err)
		}
		assertVersion30References(t, db, before)
	})

	t.Run("M10 CheckCurrent boundary rejects 30 and accepts 31", func(t *testing.T) {
		db, pool := newMulticityMigrationDatabase(t, fixture, false)
		upTo(t, db, 30)
		assertCurrent(t, pool, false)
		upAll(t, db)
		assertCurrent(t, pool, true)
	})
}

func newMulticityMigrationDatabase(t *testing.T, fixture *pgx.ConnConfig, cLocale bool) (*sql.DB, *pgxpool.Pool) {
	t.Helper()
	name := "multicity_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	if _, err := testdbguard.Parse(connectionURL(fixture, name), name); err != nil {
		t.Fatalf("generated database target is unsafe: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, fixture)
	if err != nil {
		t.Fatal("dedicated migration fixture is unavailable")
	}
	create := fmt.Sprintf("CREATE DATABASE %s TEMPLATE template0 ENCODING 'UTF8'", quoteIdentifier(name))
	if cLocale {
		create += " LC_COLLATE 'C' LC_CTYPE 'C'"
	} else {
		create += " LOCALE_PROVIDER icu ICU_LOCALE 'und'"
	}
	if _, err := admin.Exec(ctx, create); err != nil {
		admin.Close(ctx)
		t.Fatalf("create isolated migration database: %v", err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	generatedURL := connectionURL(fixture, name)
	generatedConfig, err := pgxpool.ParseConfig(generatedURL)
	if err != nil {
		t.Fatal(err)
	}
	generated := *fixture
	generated.Database = name
	db := stdlib.OpenDB(generated)
	generatedConfig.ConnConfig = &generated
	pool, err := pgxpool.NewWithConfig(ctx, generatedConfig)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_ = db.Close()
		admin, err := pgx.ConnectConfig(context.Background(), fixture)
		if err != nil {
			t.Errorf("reconnect to clean isolated migration database: %v", err)
			return
		}
		defer admin.Close(context.Background())
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+quoteIdentifier(name)); err != nil {
			t.Errorf("drop generated migration database %q: %v", name, err)
		}
	})
	return db, pool
}

func connectionURL(cfg *pgx.ConnConfig, database string) string {
	user := url.User(cfg.User)
	if cfg.Password != "" {
		user = url.UserPassword(cfg.User, cfg.Password)
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     user,
		Host:     net.JoinHostPort("127.0.0.1", "65428"),
		Path:     "/" + database,
		RawQuery: "sslmode=disable",
	}).String()
}

func quoteIdentifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func configureGoose(t *testing.T) {
	t.Helper()
	goose.SetBaseFS(Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
}

func upAll(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Up(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func upTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	configureGoose(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := goose.UpToContext(ctx, db, ".", version); err != nil {
		t.Fatalf("Up to %d: %v", version, err)
	}
}

func mustFailUp31(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Up(ctx, db); err == nil {
		t.Fatal("migration 31 accepted conflicting city metadata")
	}
	var applied bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM goose_db_version WHERE version_id=31 AND is_applied)`).Scan(&applied); err != nil || applied {
		t.Fatalf("failed migration 31 was recorded as applied=%v err=%v", applied, err)
	}
}

func assertCurrent(t *testing.T, pool *pgxpool.Pool, want bool) {
	t.Helper()
	err := CheckCurrent(context.Background(), pool)
	if want && err != nil {
		t.Fatalf("CheckCurrent rejected complete database: %v", err)
	}
	if !want && err == nil {
		t.Fatal("CheckCurrent accepted database missing migration 31")
	}
}

func assertOnlyVersionAppliedAfter30(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT version_id FROM goose_db_version WHERE id > (SELECT max(id) FROM goose_db_version WHERE version_id=30 AND is_applied) AND is_applied ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != 31 {
		t.Fatalf("versions applied after 30 = %v, want [31]", versions)
	}
}

type version30References struct{ user, event, room uuid.UUID }

func seedVersion30References(t *testing.T, db *sql.DB) version30References {
	t.Helper()
	city, user, venue, event, room, pool := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.Exec(`INSERT INTO cities(id,name,timezone,center_lat,center_lng) VALUES($1,'Upgrade fixture','Europe/Moscow',55.75,37.61)`, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,max_user_id,display_name,city_id,onboarding_state) VALUES($1,$2,'Upgrade fixture',$3,'complete')`, user, time.Now().UnixNano(), city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO venues(id,city_id,name,address,latitude,longitude,venue_type) VALUES($1,$2,'Upgrade venue','fixture',55.75,37.61,'other')`, venue, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(id,source,external_id,title,description,venue_id,starts_at,timezone,currency,ticket_available,status) VALUES($1,'user',$2,'Upgrade event','fixture',$3,now(),'Europe/Moscow','RUB',false,'published')`, event, event.String(), venue); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rooms(id,creator_user_id,city_id,name,state,expires_at,public_plan) VALUES($1,$2,$3,'Upgrade room','voting',now()+interval '1 day','{"version":1}')`, room, user, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'creator')`, room, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO room_pools(id,room_id,version,round_no,ranker_version,input_fingerprint,state,candidate_count) VALUES($1,$2,1,1,'fixture','fixture','ready',1)`, pool, room); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO room_pool_events(pool_id,event_id,position,group_score,participant_score_min,participant_score_mean,explanation,feature_snapshot) VALUES($1,$2,0,1,1,1,'{}','{}')`, pool, event); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO room_votes(pool_id,room_id,event_id,user_id,vote) VALUES($1,$2,$3,$4,'like')`, pool, room, event, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO saved_events(user_id,event_id) VALUES($1,$2)`, user, event); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,status,idempotency_key,request_hash) VALUES($1,$2,$3,now(),'Europe/Moscow','{}','pending','pending',decode(repeat('00',32),'hex'))`, uuid.New(), user, city); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event_submissions(id,author_id,city_id,starts_at,timezone,payload,status,idempotency_key,request_hash,decided_at,decided_by,event_id) VALUES($1,$2,$3,now(),'Europe/Moscow','{}','approved','approved',decode(repeat('01',32),'hex'),now(),'fixture',$4)`, uuid.New(), user, city, event); err != nil {
		t.Fatal(err)
	}
	return version30References{user: user, event: event, room: room}
}

func assertVersion30References(t *testing.T, db *sql.DB, refs version30References) {
	t.Helper()
	var publicPlan string
	var submissions, saved, votes int
	var kind string
	err := db.QueryRow(`SELECT public_plan::text FROM rooms WHERE id=$1`, refs.room).Scan(&publicPlan)
	if err != nil || publicPlan != `{"version": 1}` {
		t.Fatalf("public plan changed: %q %v", publicPlan, err)
	}
	err = db.QueryRow(`SELECT (SELECT count(*) FROM event_submissions WHERE author_id=$1), (SELECT count(*) FROM saved_events WHERE user_id=$1 AND event_id=$2), (SELECT count(*) FROM room_votes WHERE user_id=$1 AND event_id=$2), (SELECT source_kind FROM event_sources WHERE source_key='user')`, refs.user, refs.event).Scan(&submissions, &saved, &votes, &kind)
	if err != nil || submissions != 2 || saved != 1 || votes != 1 || kind != "system" {
		t.Fatalf("version 30 data changed submissions=%d saved=%d votes=%d system=%q err=%v", submissions, saved, votes, kind, err)
	}
}

func insertUser(t *testing.T, db *sql.DB, city uuid.UUID) uuid.UUID {
	t.Helper()
	user := uuid.New()
	if _, err := db.Exec(`INSERT INTO users(id,max_user_id,display_name,city_id,onboarding_state) VALUES($1,$2,'Kazan reference',$3,'complete')`, user, time.Now().UnixNano(), city); err != nil {
		t.Fatal(err)
	}
	return user
}
