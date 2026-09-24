package main

import (
	"context"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestPosterCacheReusesCanonicalImage(t *testing.T) {
	cache := make(posterCache)
	calls := 0
	fetch := func(context.Context, int64) ([]providers.NormalizedImage, error) {
		calls++
		return []providers.NormalizedImage{{URL: "https://ucare.timepad.ru/poster.jpg", Role: "card"}}, nil
	}
	for range 3 {
		images, err := cache.get(context.Background(), 2956317, fetch)
		if err != nil || len(images) != 1 {
			t.Fatalf("images=%v error=%v", images, err)
		}
	}
	if calls != 1 {
		t.Fatalf("API fetches=%d, want 1", calls)
	}
}
