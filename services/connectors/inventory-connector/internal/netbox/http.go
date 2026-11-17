package netbox

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"time"
)

type authTransport struct {
	token string
	inner http.RoundTripper
}

func (t *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.token != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Token "+t.token)
	}
	return t.inner.RoundTrip(r)
}

func isRetryableErr(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr) && (urlErr.Temporary() || urlErr.Timeout())
}

func isRetryableStatus(code int) bool {
	switch code {
	case 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// backoff returns exponential delay with ±20% jitter, capped at 16s.
func backoff(attempt int) time.Duration {
	d := time.Second
	for range attempt {
		d *= 2
	}
	if d > 16*time.Second {
		d = 16 * time.Second
	}
	//nolint:gosec // G404: math/rand is sufficient for backoff jitter; cryptographic randomness is not required and just adds unnecessary complexity.
	jitter := float64(d) * 0.2 * (rand.Float64()*2 - 1)
	return d + time.Duration(jitter)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
