// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package drain

import (
	"context"
	"math/rand/v2"
	"time"
)

// backoff produces an exponentially growing delay with full-range jitter,
// capped at max. It is reset after a successful delivery. It is not safe for
// concurrent use; each worker owns its own.
type backoff struct {
	initial time.Duration
	max     time.Duration
	jitter  float64 // fraction in [0,1]; delay varies by +/- jitter around the current step
	cur     time.Duration
}

func newBackoff(initial, max time.Duration, jitter float64) *backoff {
	return &backoff{initial: initial, max: max, jitter: jitter}
}

// next advances the backoff and returns the next delay, never exceeding max.
func (b *backoff) next() time.Duration {
	if b.cur <= 0 {
		b.cur = b.initial
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}

	d := b.cur
	if b.jitter > 0 {
		delta := float64(d) * b.jitter
		d = time.Duration(float64(d) - delta + rand.Float64()*2*delta)
	}
	if d > b.max {
		d = b.max
	}
	if d < 0 {
		d = 0
	}
	return d
}

// reset returns the backoff to its initial state after a success.
func (b *backoff) reset() { b.cur = 0 }

// sleepCtx waits for d or until ctx is done. It returns true if the full delay
// elapsed and false if the context was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
