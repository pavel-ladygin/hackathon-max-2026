// Command import-timepad-snapshot imports previously captured Timepad API
// pages into a local PostgreSQL catalog without contacting Timepad.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/timepad"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("import-timepad-snapshot", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	snapshotDir := flags.String("snapshot-dir", "", "directory containing page-<skip>.json files")
	databaseURL := flags.String("database-url", strings.TrimSpace(os.Getenv("DATABASE_URL")), "PostgreSQL connection URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(*snapshotDir) == "" {
		return errors.New("--snapshot-dir is required")
	}
	if strings.TrimSpace(*databaseURL) == "" {
		return errors.New("--database-url or DATABASE_URL is required")
	}
	paths, err := snapshotFiles(*snapshotDir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, *databaseURL)
	if err != nil {
		return fmt.Errorf("open snapshot importer database: %w", err)
	}
	defer db.Close()
	if err := migrations.CheckCurrent(ctx, db.Pool); err != nil {
		return errors.New("snapshot import requires current migrations; run cmd/migrate first")
	}
	cityID := uuid.MustParse(catalogseed.MoscowCityID)
	var exists bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM cities WHERE id=$1)", cityID).Scan(&exists); err != nil {
		return fmt.Errorf("check Moscow catalog city: %w", err)
	}
	if !exists {
		return errors.New("Moscow catalog city is missing; initialize the catalog first")
	}

	stats, err := timepad.ImportSnapshotFiles(ctx, paths, cityID, providers.NewRepository(db), func(err error) {
		fmt.Fprintln(os.Stderr, err)
	})
	fmt.Printf("pages=%d raw=%d matched=%d normalized=%d inserted=%d updated=%d skipped=%d errors=%d\n",
		stats.PagesFetched, stats.Fetched, stats.Matched, stats.Normalized, stats.Inserted, stats.Updated, stats.Skipped, stats.Errors)
	return err
}

func snapshotFiles(directory string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "page-*.json"))
	if err != nil {
		return nil, fmt.Errorf("find timepad snapshot pages: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("snapshot directory contains no page-*.json files")
	}
	type page struct {
		path string
		skip int
	}
	pages := make([]page, 0, len(paths))
	for _, path := range paths {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "page-"), ".json")
		skip, err := strconv.Atoi(name)
		if err != nil || skip < 0 {
			return nil, fmt.Errorf("invalid snapshot page name %q", filepath.Base(path))
		}
		pages = append(pages, page{path: path, skip: skip})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].skip < pages[j].skip })
	for i := range pages {
		paths[i] = pages[i].path
	}
	return paths, nil
}
