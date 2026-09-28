package eventsources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
)

func TestManualSyncReservesRunnerLockBeforeReturningAccepted(t *testing.T) {
	db := openEventSourcesTestDB(t)
	ctx := context.Background()
	codec, err := sourceconfig.NewSecretCodec([]byte("0123456789abcdef0123456789abcdef"), 1)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := sourceconfig.NewRepository(db, codec)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := "generic:runner-handler-" + uuid.NewString()
	input := handlerTestInput(sourceKey, "runner-secret", "event-"+uuid.NewString())
	source, err := sources.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM provider_sync_runs WHERE provider=$1`, sourceKey)
		_, _ = db.Exec(ctx, `DELETE FROM event_sources WHERE id=$1`, source.ID)
	})
	handler, err := NewHandler(ctx, db, sources)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://admin.example.test/api/v1/internal/event-sources/"+source.ID.String()+"/sync", nil)
		req.Header.Set("X-Admin-Request", "1")
		req.Header.Set("Origin", "https://admin.example.test")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}

	lock, err := acquireSourceSyncLock(ctx, db, sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	conflict := request()
	if conflict.Code != http.StatusConflict {
		lock.Release()
		t.Fatalf("sync conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	lock.Release()

	started := make(chan struct{}, 1)
	handler.runner.importFn = func(context.Context, uuid.UUID, sourceconfig.Source, string, providers.SyncStore) (providers.ImportStats, error) {
		started <- struct{}{}
		return providers.ImportStats{}, nil
	}
	accepted := request()
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("sync accepted status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner import did not start")
	}
}
