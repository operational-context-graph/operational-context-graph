package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCodecEncodesDeterministicImmutableAndMutableSnapshots verifies stable tokens, preserved snapshot coordinates, and independent decoded filter maps.
func TestCodecEncodesDeterministicImmutableAndMutableSnapshots(t *testing.T) {
	codec := New()
	immutable := testPayload("release:1.0.0")
	mutable := testPayload("activation-sequence:42")
	mutable.Filters = map[string]string{"kind": "schema", "class": "entity-type"}

	first, err := codec.Encode(mutable)
	require.NoError(t, err)
	second, err := codec.Encode(Payload{
		Resource: mutable.Resource,
		Filters:  map[string]string{"class": "entity-type", "kind": "schema"},
		Limit:    mutable.Limit,
		Sort:     mutable.Sort,
		Last:     mutable.Last,
		Snapshot: mutable.Snapshot,
	})
	require.NoError(t, err)
	assert.Equal(t, first, second)

	token, err := codec.Encode(immutable)
	require.NoError(t, err)
	value, err := codec.Decode(token, immutable)
	require.NoError(t, err)
	assert.Equal(t, immutable.Snapshot, value.Snapshot)
	value.Filters["kind"] = "changed"
	again, err := codec.Decode(token, immutable)
	require.NoError(t, err)
	assert.Equal(t, "schema", again.Filters["kind"])
}

// TestCodecBindsParametersButDoesNotAuthenticateValidState verifies changed request bindings fail while valid caller-constructed continuation coordinates remain acceptable.
func TestCodecBindsParametersButDoesNotAuthenticateValidState(t *testing.T) {
	codec := New()
	expected := testPayload("release:1.0.0")
	token, err := codec.Encode(expected)
	require.NoError(t, err)

	for _, changed := range []Payload{
		{Resource: "artifacts", Filters: expected.Filters, Limit: expected.Limit, Sort: expected.Sort},
		{Resource: expected.Resource, Filters: expected.Filters, Limit: 1, Sort: expected.Sort},
		{Resource: expected.Resource, Filters: expected.Filters, Limit: expected.Limit, Sort: "name"},
		{Resource: expected.Resource, Filters: map[string]string{"kind": "context"}, Limit: expected.Limit, Sort: expected.Sort},
	} {
		_, decodeErr := codec.Decode(token, changed)
		assert.ErrorIs(t, decodeErr, ErrInvalid)
	}

	callerConstructed := expected
	callerConstructed.Last = "different-keyset-position"
	callerToken, err := codec.Encode(callerConstructed)
	require.NoError(t, err)
	decoded, err := codec.Decode(callerToken, expected)
	require.NoError(t, err)
	assert.Equal(t, callerConstructed.Last, decoded.Last)
}

// TestCodecRejectsMalformedDuplicateAndUnsupportedPayloads verifies malformed encodings, duplicate or unknown fields, and unsupported versions return ErrInvalid.
func TestCodecRejectsMalformedDuplicateAndUnsupportedPayloads(t *testing.T) {
	codec := New()
	expected := testPayload("release:1.0.0")
	for _, raw := range []string{
		`{"v":1,"r":"releases","r":"artifacts","f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":1,"r":"releases","f":{"kind":"schema","kind":"context"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":2,"r":"releases","f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"r":"releases","v":1,"f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{}`, `[]`, `null`, `{"v":1}`,
		`{"v":"1","r":"releases","f":{},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":1,"r":"releases","f":{"kind":1},"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":1,"r":"releases","f":null,"l":1.5,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":1,"r":"releases","f":{},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","unknown":"value"}`,
	} {
		token := base64.RawURLEncoding.EncodeToString([]byte(raw))
		_, err := codec.Decode(token, expected)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	for _, token := range []string{"", "not-a-token", "a.b", strings.Repeat("x", maxTokenBytes+1)} {
		_, err := codec.Decode(token, expected)
		assert.ErrorIs(t, err, ErrInvalid)
	}
}

// TestCodecValidatesInputsAndTokenBoundaries verifies invalid inputs fail,
// exact-size tokens decode, and oversized tokens fail.
func TestCodecValidatesInputsAndTokenBoundaries(t *testing.T) {
	codec := New()
	_, err := codec.Encode(Payload{Resource: "releases", Limit: 100, Sort: "sequence", Last: "after"})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = codec.Decode("token", Payload{})
	assert.ErrorIs(t, err, ErrInvalid)

	exact := testPayload("release:1.0.0")
	for length := 1; length <= maxTokenBytes; length++ {
		exact.Last = strings.Repeat("x", length)
		token, encodeErr := codec.Encode(exact)
		if encodeErr == nil && len(token) == maxTokenBytes {
			decoded, decodeErr := codec.Decode(token, testPayload("release:1.0.0"))
			require.NoError(t, decodeErr)
			assert.Equal(t, exact.Last, decoded.Last)
			break
		}
		if length == maxTokenBytes {
			t.Fatal("could not construct an exact-size cursor")
		}
	}

	oversized := testPayload("release:1.0.0")
	oversized.Last = strings.Repeat("x", maxTokenBytes)
	_, err = codec.Encode(oversized)
	assert.ErrorIs(t, err, ErrInvalid)
}

// TestCodecRejectsInvalidFieldShapes verifies every cursor field rejects wrong JSON types, invalid values, and noncanonical representations.
func TestCodecRejectsInvalidFieldShapes(t *testing.T) {
	expected := testPayload("release:1.0.0")
	// Field order matches Payload so canonical encoding cannot mask invalid field shapes.
	validDocument := struct {
		Version  int               `json:"v"`
		Resource any               `json:"r"`
		Filters  map[string]string `json:"f"`
		Limit    int               `json:"l"`
		Sort     any               `json:"s"`
		Last     any               `json:"a"`
		Snapshot any               `json:"n"`
	}{
		Version:  1,
		Resource: expected.Resource,
		Filters:  expected.Filters,
		Limit:    expected.Limit,
		Sort:     expected.Sort,
		Last:     expected.Last,
		Snapshot: expected.Snapshot,
	}
	controlRaw, marshalErr := json.Marshal(validDocument)
	require.NoError(t, marshalErr)
	codec := New()
	control, decodeErr := codec.Decode(base64.RawURLEncoding.EncodeToString(controlRaw), expected)
	require.NoError(t, decodeErr, "the unchanged fixture must be a valid canonical cursor")
	want := expected
	want.Version = 1
	assert.Equal(t, want, control)

	for _, key := range []string{"r", "s", "a", "n"} {
		for _, value := range []any{"", nil, 1, []any{}} {
			t.Run(key+"/"+fmt.Sprint(value), func(t *testing.T) {
				document := validDocument
				switch key {
				case "r":
					document.Resource = value
				case "s":
					document.Sort = value
				case "a":
					document.Last = value
				case "n":
					document.Snapshot = value
				}
				raw, err := json.Marshal(document)
				require.NoError(t, err)
				_, err = codec.Decode(base64.RawURLEncoding.EncodeToString(raw), expected)
				require.ErrorIs(t, err, ErrInvalid)
			})
		}
	}
	for _, raw := range []string{
		`{"v":1,"r":"releases","f":null,"l":100,"s":"sequence","a":"after","n":"release:1.0.0"}`,
		`{"v":1,"r":"releases","f":{},"l":0,"s":"sequence","a":"after","n":"release:1.0.0"}`,
	} {
		_, err := New().Decode(base64.RawURLEncoding.EncodeToString([]byte(raw)), testPayload("release:1.0.0"))
		require.ErrorIs(t, err, ErrInvalid)
	}
	value := testPayload("release:1.0.0")
	token, err := New().Encode(value)
	require.NoError(t, err)
	value.Filters = map[string]string{}
	_, err = New().Decode(token, value)
	require.ErrorIs(t, err, ErrInvalid)

	// Raw JSON fits, but Base64url expansion exceeds the public token limit.
	value = testPayload("release:1.0.0")
	value.Last = strings.Repeat("x", maxTokenBytes*3/4)
	_, err = New().Encode(value)
	require.ErrorIs(t, err, ErrInvalid)
}

// TestCodecIsSafeForConcurrentCalls verifies concurrent decoding of one encoded
// token preserves its snapshot coordinate without errors.
func TestCodecIsSafeForConcurrentCalls(t *testing.T) {
	codec := New()
	expected := testPayload("release:1.0.0")
	token, err := codec.Encode(expected)
	require.NoError(t, err)

	var group sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for range 32 {
		group.Go(func() {
			value, decodeErr := codec.Decode(token, expected)
			if decodeErr != nil {
				errorsSeen <- decodeErr
			} else if value.Snapshot != expected.Snapshot {
				errorsSeen <- errors.New("decoded snapshot changed")
			}
		})
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		require.NoError(t, err)
	}
}

// testPayload supplies valid request bindings and a continuation position for the chosen snapshot coordinate.
func testPayload(snapshot string) Payload {
	return Payload{
		Resource: "releases",
		Filters:  map[string]string{"kind": "schema"},
		Limit:    100,
		Sort:     "sequence",
		Last:     "after",
		Snapshot: snapshot,
	}
}
