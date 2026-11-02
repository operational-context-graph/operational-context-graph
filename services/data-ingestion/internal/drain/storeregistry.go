package drain

import (
	"fmt"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/store"
)

// StoreRegistry maps a StoreID to the StoreWriter that serves it.
type StoreRegistry struct {
	writers map[ingest.StoreID]store.StoreWriter
}

func NewStoreRegistry() *StoreRegistry {
	return &StoreRegistry{writers: make(map[ingest.StoreID]store.StoreWriter)}
}

// Register adds a StoreWriter. It rejects a writer whose BatchLimits are invalid and a duplicate StoreID.
func (r *StoreRegistry) Register(w store.StoreWriter) error {
	id := w.StoreID()
	if _, dup := r.writers[id]; dup {
		return fmt.Errorf("drain: duplicate StoreID %q", id)
	}
	if err := w.Limits().Validate(); err != nil {
		return fmt.Errorf("drain: store %q: %w", id, err)
	}
	r.writers[id] = w
	return nil
}

// Writer returns the StoreWriter for id, if registered.
func (r *StoreRegistry) Writer(id ingest.StoreID) (store.StoreWriter, bool) {
	w, ok := r.writers[id]
	return w, ok
}

// Len reports how many writers are registered.
func (r *StoreRegistry) Len() int { return len(r.writers) }
