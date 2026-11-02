package store

import "errors"

// ErrOverloaded reports transient store back-pressure: the batch was not
// accepted and the drain should pause this store and retry the same pinned
// batch later. It is DISTINCT from ingest.ErrOverloaded, which reports that the
// synchronous ingest pipeline is overloaded. Callers MUST match it with
// errors.Is; never match on message text.
var ErrOverloaded = errors.New("store overloaded")

// ErrInvalidLimits classifies a BatchLimits value the drain cannot use.
// Callers MUST match it with errors.Is.
var ErrInvalidLimits = errors.New("invalid batch limits")

// PermanentError reports a batch the store will never accept as written (for
// example a store-side rejection).
// Callers MUST match it with errors.As; never match on message text.
type PermanentError struct {
	Reason string
}

func (e *PermanentError) Error() string { return e.Reason }
