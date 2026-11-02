package drain

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capHandler is a slog.Handler that records emitted messages for assertions.
type capHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (h *capHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	h.msgs = append(h.msgs, b.String())
	return nil
}

func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capHandler) WithGroup(string) slog.Handler      { return h }

func (h *capHandler) contains(sub string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func runDrainWithLogger(t *testing.T, ob Outbox, reg *StoreRegistry, cfg Config, log *slog.Logger) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := New(ob, reg, cfg, log)
	done := make(chan struct{})
	go func() {
		_ = d.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func TestRouting_UnregisteredTargetStore_ParkedAndLogged(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("unregistered-store", 2, 10)

	cap := &capHandler{}
	// Registry has a different store, so the seeded rows have no writer.
	reg := registryWith(t, &fakeWriter{id: "telemetry", limits: testLimits()})

	runDrainWithLogger(t, ob, reg, fastConfig(), slog.New(cap))

	require.Eventually(t, func() bool { return cap.contains("unregistered-store") }, time.Second, 2*time.Millisecond,
		"unroutable store should be logged")
	assert.Equal(t, 2, ob.total(), "unroutable rows stay pending, never deleted")
	assert.Equal(t, 2, ob.pendingCount("unregistered-store"))
}

func TestRouting_EmptyTargetStore_ParkedAndLogged(t *testing.T) {
	ob := &fakeOutbox{}
	ob.seed("", 1, 10) // empty target_store

	cap := &capHandler{}
	runDrainWithLogger(t, ob, NewStoreRegistry(), fastConfig(), slog.New(cap))

	require.Eventually(t, func() bool { return cap.contains("target_store=") }, time.Second, 2*time.Millisecond)
	assert.Equal(t, 1, ob.total(), "row with empty target_store stays pending")
}
