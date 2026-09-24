package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestRunCandidateBatchBoundsConcurrency(t *testing.T) {
	candidates := make([]candidate, 24)
	started := make(chan struct{}, len(candidates))
	release := make(chan struct{})
	var active, maximum atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- runCandidateBatch(context.Background(), candidates, 8, func(context.Context, candidate) {
			current := active.Add(1)
			for previous := maximum.Load(); current > previous; previous = maximum.Load() {
				if maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
		})
	}()
	for range 8 {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("worker pool did not start 8 candidates")
		}
	}
	if got := maximum.Load(); got != 8 {
		close(release)
		t.Fatalf("maximum concurrent candidates = %d, want 8", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := maximum.Load(); got > 8 {
		t.Fatalf("maximum concurrent candidates = %d, want <= 8", got)
	}
}

func TestContinuousPosterCacheCoalescesConcurrentFetches(t *testing.T) {
	cache := newContinuousPosterCache()
	var calls atomic.Int32
	start := make(chan struct{})
	fetch := func(context.Context, int64) ([]providers.NormalizedImage, error) {
		calls.Add(1)
		<-start
		return []providers.NormalizedImage{{URL: "https://example.test/poster.jpg"}}, nil
	}

	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			images, err := cache.get(context.Background(), 42, fetch)
			if err == nil && (len(images) != 1 || images[0].URL != "https://example.test/poster.jpg") {
				err = errors.New("cache returned unexpected image")
			}
			errs <- err
		}()
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}

func TestContinuousPosterCacheRetriesFailedFetch(t *testing.T) {
	cache := newContinuousPosterCache()
	var calls int
	fetch := func(context.Context, int64) ([]providers.NormalizedImage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary failure")
		}
		return nil, nil
	}
	if _, err := cache.get(context.Background(), 9, fetch); err == nil {
		t.Fatal("first fetch should fail")
	}
	if _, err := cache.get(context.Background(), 9, fetch); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
}

func TestSerializedPacerSpacesConcurrentRequests(t *testing.T) {
	pacer := &serializedPacer{interval: 15 * time.Millisecond}
	const callers = 4
	starts := make([]time.Time, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := pacer.request(context.Background(), nil, func(context.Context) error {
				starts[i] = time.Now()
				return nil
			}); err != nil {
				t.Errorf("pacer wait: %v", err)
			}
		}(i)
	}
	wg.Wait()
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < pacer.interval-2*time.Millisecond {
			t.Fatalf("request starts were too close: %s", gap)
		}
	}
}

func TestSerializedPacerHonorsCancellation(t *testing.T) {
	pacer := &serializedPacer{interval: time.Hour}
	if err := pacer.request(context.Background(), nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pacer.request(ctx, nil, func(context.Context) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context canceled", err)
	}
}
