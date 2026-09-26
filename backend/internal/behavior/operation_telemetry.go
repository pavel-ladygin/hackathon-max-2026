package behavior

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

// OperationEventSink receives already-bounded operational facts. Implementors
// must be best effort because these events are not part of the API result.
type OperationEventSink func(context.Context, contracts.ServerBehaviorEvent)

type operationQueue struct {
	events chan contracts.ServerBehaviorEvent
	done   chan struct{}
	mu     sync.RWMutex
	closed bool
}

var (
	operationQueuesMu sync.Mutex
	operationQueues   []*operationQueue
)

// OperationMiddleware records bounded server performance facts for one fixed
// operation label. It must be installed after authentication middleware.
func OperationMiddleware(operation string, sink OperationEventSink) func(http.Handler) http.Handler {
	if !allowedServerOperation(operation) {
		panic("invalid analytics operation label")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			observed := &operationStatusWriter{ResponseWriter: w}
			emit := func(status int) {
				if sink == nil {
					return
				}
				principal, ok := contracts.PrincipalFromContext(r.Context())
				if !ok || principal.UserID == uuid.Nil {
					return
				}
				if status == 0 {
					status = http.StatusOK
				}
				duration := time.Since(started).Milliseconds()
				if duration < 0 {
					duration = 0
				}
				if duration > 600000 {
					duration = 600000
				}
				requestID := httpapi.RequestID(r.Context())
				for _, eventType := range operationEventTypes(status) {
					properties, err := json.Marshal(map[string]any{"operation": operation, "status_code": status, "duration_ms": duration, "critical": true})
					if err != nil {
						slog.Default().Error("analytics operation properties encode failed", "operation", operation, "request_id", requestID)
						continue
					}
					sink(r.Context(), contracts.ServerBehaviorEvent{
						ID: uuid.New(), UserID: principal.UserID, Type: eventType,
						RequestID: requestID, OccurredAt: time.Now().UTC(), Properties: properties,
						DeduplicationKey: "api/" + requestID + "/" + eventType,
					})
				}
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					emit(http.StatusInternalServerError)
					panic(recovered)
				}
				emit(observed.status)
			}()
			next.ServeHTTP(observed, r)
		})
	}
}

// DatabaseOperationSink persists the telemetry in an independent bounded
// transaction, after the product handler has completed.
func DatabaseOperationSink(db *store.Pool, recorder contracts.BehaviorRecorder) OperationEventSink {
	if db == nil || recorder == nil {
		return func(context.Context, contracts.ServerBehaviorEvent) {}
	}
	queue := newOperationQueue(func(event contracts.ServerBehaviorEvent) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			return recorder.Record(ctx, tx, event)
		})
		cancel()
		if err != nil {
			slog.Default().Error("analytics operation event write failed", "event_type", event.Type, "request_id", event.RequestID, "error", err)
		}
	})
	operationQueuesMu.Lock()
	operationQueues = append(operationQueues, queue)
	operationQueuesMu.Unlock()
	return queue.Sink()
}

func asynchronousOperationSink(write func(contracts.ServerBehaviorEvent)) OperationEventSink {
	return newOperationQueue(write).Sink()
}

func newOperationQueue(write func(contracts.ServerBehaviorEvent)) *operationQueue {
	queue := &operationQueue{events: make(chan contracts.ServerBehaviorEvent, 256), done: make(chan struct{})}
	go func() {
		defer close(queue.done)
		for event := range queue.events {
			write(event)
		}
	}()
	return queue
}

func (queue *operationQueue) Sink() OperationEventSink {
	return func(_ context.Context, event contracts.ServerBehaviorEvent) {
		queue.mu.RLock()
		defer queue.mu.RUnlock()
		if queue.closed {
			slog.Default().Warn("analytics operation event dropped", "event_type", event.Type, "request_id", event.RequestID)
			return
		}
		select {
		case queue.events <- event:
		default:
			slog.Default().Warn("analytics operation event dropped", "event_type", event.Type, "request_id", event.RequestID)
		}
	}
}

func (queue *operationQueue) Close() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if !queue.closed {
		queue.closed = true
		close(queue.events)
	}
}

// FlushOperationTelemetry closes the registered bounded queues and waits for
// their workers to drain before the database pool is closed.
func FlushOperationTelemetry(ctx context.Context) {
	operationQueuesMu.Lock()
	queues := operationQueues
	operationQueues = nil
	operationQueuesMu.Unlock()
	for _, queue := range queues {
		queue.Close()
	}
	for _, queue := range queues {
		select {
		case <-queue.done:
		case <-ctx.Done():
			slog.Default().Warn("analytics operation queue drain timed out", "pending_events", len(queue.events), "error", ctx.Err())
			return
		}
	}
}

func operationEventTypes(status int) []string {
	if status >= http.StatusBadRequest {
		return []string{"api_performance", "api_error"}
	}
	return []string{"api_performance"}
}

func allowedServerOperation(operation string) bool {
	switch operation {
	case "room_create", "room_join", "room_vote", "feed_load":
		return true
	default:
		return false
	}
}

type operationStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *operationStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *operationStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *operationStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
