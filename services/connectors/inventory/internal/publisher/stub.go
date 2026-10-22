package publisher

import (
	"context"
	"sync"

	"github.com/operational-context-graph/operational-context-graph/services/connectors/inventory/internal/envelope"
)

type StubPublisher struct {
	mu   sync.Mutex
	msgs []envelope.Message
}

func (s *StubPublisher) Publish(_ context.Context, msgs []envelope.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msgs...)
	return nil
}

func (s *StubPublisher) Messages() []envelope.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]envelope.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}
