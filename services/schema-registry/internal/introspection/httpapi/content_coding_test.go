package httpapi

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNegotiateCoding verifies identity defaults, gzip preferences, wildcard and explicit exclusions, and quality-based supported coding selection.
func TestNegotiateCoding(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		want   ContentCoding
		ok     bool
	}{
		{"absent", "", Identity, true},
		{"empty", "   ", Identity, true},
		{"identity only", "identity", Identity, true},
		{"gzip only", "gzip", Gzip, true},
		{"gzip preferred", "gzip;q=0.9, identity;q=0.1", Gzip, true},
		{"identity preferred", "gzip;q=0.1, identity;q=0.9", Identity, true},
		{"case insensitive", "GZip;Q=1, identity;q=0", Gzip, true},
		{"both forbidden", "gzip;q=0, identity;q=0", "", false},
		{"wildcard forbidden", "*;q=0", "", false},
		{"specific gzip prohibition", "gzip;q=0, *;q=1", Identity, true},
		{"specific identity prohibition", "identity;q=0, *;q=1", Gzip, true},
		{"implicit identity after gzip prohibition", "gzip;q=0", Identity, true},
		{"implicit identity for unsupported coding", "br", Identity, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NegotiateCoding(test.header)
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.ok, ok)
		})
	}
}

// TestEncodeRepresentationIsDeterministicAndExact verifies identity copies preserve bytes and repeated gzip encoding produces identical, losslessly decoded streams.
func TestEncodeRepresentationIsDeterministicAndExact(t *testing.T) {
	identity := bytes.Repeat([]byte("x"), 64*1024)
	first, err := EncodeRepresentation(identity, Gzip)
	require.NoError(t, err)
	second, err := EncodeRepresentation(identity, Gzip)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	decoded, err := decompress(first)
	require.NoError(t, err)
	assert.Equal(t, identity, decoded)
	plain, err := EncodeRepresentation(identity, Identity)
	require.NoError(t, err)
	assert.Equal(t, identity, plain)
	assert.Equal(t, []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 4, 255}, first[:10])
}

// TestEncodeRepresentationContextStopsCancelledCompression verifies canceled contexts stop representation work with context.Canceled and no encoded output.
func TestEncodeRepresentationContextStopsCancelledCompression(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := EncodeRepresentationContext(ctx, bytes.Repeat([]byte("x"), 16*1024*1024), Gzip)
	require.ErrorIs(t, err, context.Canceled)
}

// TestConcurrentCancelledCompressionReleasesWorkers verifies simultaneous canceled encoding requests terminate without stranded compression workers.
func TestConcurrentCancelledCompressionReleasesWorkers(t *testing.T) {
	const workers = 8
	body := bytes.Repeat([]byte("x"), 16*1024*1024)
	errorsSeen := make(chan error, workers)
	for range workers {
		go func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := EncodeRepresentationContext(ctx, body, Gzip)
			errorsSeen <- err
		}()
	}
	for range workers {
		assert.True(t, errors.Is(<-errorsSeen, context.Canceled))
	}
}
