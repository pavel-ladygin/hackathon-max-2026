// Command seed installs or refreshes the deterministic demo catalog.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if strings.TrimSpace(os.Getenv("APP_ENV")) != "local" && strings.TrimSpace(os.Getenv("APP_ENV")) != "test" {
		return fmt.Errorf("demo seed requires APP_ENV=local or test")
	}
	base, err := catalogseed.EffectiveBaseDate(os.Getenv("DEMO_BASE_DATE"), time.Now())
	if err != nil {
		return err
	}
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		return fmt.Errorf("open seed database failed")
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return fmt.Errorf("seed requires current migrations; run cmd/migrate first")
	}
	counts, err := catalogseed.Apply(ctx, db, base)
	if err != nil {
		return fmt.Errorf("apply demo catalog failed")
	}
	fmt.Printf("DEMO_BASE_DATE=%s city=%d metro=%d venues=%d categories=%d events=%d images=%d\n", base.Format(time.DateOnly), counts.Cities, counts.MetroStations, counts.Venues, counts.Categories, counts.Events, counts.Images)
	return nil
}
