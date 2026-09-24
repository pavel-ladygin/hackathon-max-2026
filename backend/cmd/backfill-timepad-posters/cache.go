package main

import (
	"context"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

type posterCache map[int64][]providers.NormalizedImage

func (cache posterCache) get(ctx context.Context, canonicalID int64, fetch func(context.Context, int64) ([]providers.NormalizedImage, error)) ([]providers.NormalizedImage, error) {
	if images, exists := cache[canonicalID]; exists {
		return images, nil
	}
	images, err := fetch(ctx, canonicalID)
	if err == nil {
		cache[canonicalID] = images
	}
	return images, err
}
