// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodecEncodesDeterministicImmutableAndMutableSnapshots(t *testing.T) {
	now := testTime()
	codec := newCodec(t, testKey("current", "a", now.Add(time.Hour)), nil, now)
	immutable := testPayload("release:1.0.0")
	mutable := testPayload("activation-sequence:42")
	mutable.Filters = map[string]string{"kind": "schema", "class": "entity-type"}

	first, err := codec.Encode(mutable)
	require.NoError(t, err)
	second, err := codec.Encode(Payload{
		Resource: mutable.Resource, Filters: map[string]string{"class": "entity-type", "kind": "schema"}, Limit: mutable.Limit,
		Sort: mutable.Sort, Last: mutable.Last, Snapshot: mutable.Snapshot,
	})
	require.NoError(t, err)
	assert.Equal(t, first, second)

	decodedImmutable, err := codec.Encode(immutable)
	require.NoError(t, err)
	value, err := codec.Decode(decodedImmutable, immutable)
	require.NoError(t, err)
	assert.Equal(t, immutable.Snapshot, value.Snapshot)
	value.Filters["kind"] = "changed"
	again, err := codec.Decode(decodedImmutable, immutable)
	require.NoError(t, err)
	assert.Equal(t, "schema", again.Filters["kind"])
}

func TestCodecRejectsTamperingAndParameterBindingChanges(t *testing.T) {
	now := testTime()
	codec := newCodec(t, testKey("current", "a", now.Add(time.Hour)), nil, now)
	expected := testPayload("release:1.0.0")
	token, err := codec.Encode(expected)
	require.NoError(t, err)

	for _, altered := range []string{token + "x", token[:len(token)-1] + "x", strings.Replace(token, ".", "x.", 1)} {
		_, err := codec.Decode(altered, expected)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	for _, changed := range []Payload{
		{Resource: "artifacts", Filters: expected.Filters, Limit: expected.Limit, Sort: expected.Sort},
		{Resource: expected.Resource, Filters: expected.Filters, Limit: 1, Sort: expected.Sort},
		{Resource: expected.Resource, Filters: expected.Filters, Limit: expected.Limit, Sort: "name"},
		{Resource: expected.Resource, Filters: map[string]string{"kind": "context"}, Limit: expected.Limit, Sort: expected.Sort},
	} {
		_, err := codec.Decode(token, changed)
		assert.ErrorIs(t, err, ErrInvalid)
	}
}

func TestCodecRejectsMalformedDuplicateAndUnsupportedPayloads(t *testing.T) {
	now := testTime()
	key := testKey("current", "a", now.Add(time.Hour))
	codec := newCodec(t, key, nil, now)
	expected := testPayload("release:1.0.0")
	deadline := key.Deadline.Format(time.RFC3339Nano)

	duplicate := `{"v":1,"k":"current","r":"releases","r":"artifacts","f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`
	duplicateFilter := `{"v":1,"k":"current","r":"releases","f":{"kind":"schema","kind":"context"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`
	unsupported := `{"v":2,"k":"current","r":"releases","f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`
	nonCanonical := `{"r":"releases","v":1,"k":"current","f":{"kind":"schema"},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`
	for _, raw := range []string{
		duplicate, duplicateFilter, unsupported, nonCanonical, `{}`, `[]`, `{"v":1}`,
		`{"v":"1","k":"current","r":"releases","f":{},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`,
		`{"v":1,"k":"current","r":"releases","f":{"kind":1},"l":100,"s":"sequence","a":"after","n":"release:1.0.0","d":"` + deadline + `"}`,
		`{"v":1,"k":"current","r":"releases","f":null,"l":1.5,"s":"sequence","a":"after","n":"release:1.0.0","d":"not-a-time"}`,
	} {
		_, err := codec.Decode(signedToken(key, []byte(raw)), expected)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	for _, token := range []string{"", "not-a-token", ".", "a.b.c", strings.Repeat("x", maxTokenBytes+1)} {
		_, err := codec.Decode(token, expected)
		assert.ErrorIs(t, err, ErrInvalid)
	}
}

func TestCodecRejectsInvalidInputsAndFixedDeadlineMismatch(t *testing.T) {
	now := testTime()
	key := testKey("current", "a", now.Add(time.Hour))
	codec := newCodec(t, key, nil, now)
	_, err := codec.Encode(Payload{Resource: "releases", Limit: 100, Sort: "sequence", Last: "after"})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = codec.Decode("token", Payload{})
	assert.ErrorIs(t, err, ErrInvalid)

	payload := testPayload("release:1.0.0")
	payload.Version = cursorVersion
	payload.KeyID = key.ID
	payload.Deadline = key.Deadline.Add(-time.Minute)
	data := mustPayloadJSON(t, key, payload)
	// Sign a structurally valid payload with a deadline that differs from the
	// configured fixed key deadline.
	data = []byte(strings.Replace(string(data), key.Deadline.Format(time.RFC3339Nano), key.Deadline.Add(-time.Minute).Format(time.RFC3339Nano), 1))
	_, err = codec.Decode(signedToken(key, data), testPayload("release:1.0.0"))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestCodecSupportsRotationAndRejectsRemovedOrUnknownKeys(t *testing.T) {
	now := testTime()
	old := testKey("old", "a", now.Add(time.Hour))
	current := testKey("current", "b", now.Add(2*time.Hour))
	expected := testPayload("release:1.0.0")
	oldCodec := newCodec(t, old, nil, now)
	oldToken, err := oldCodec.Encode(expected)
	require.NoError(t, err)

	rotated := newCodec(t, current, []Key{old}, now)
	_, err = rotated.Decode(oldToken, expected)
	require.NoError(t, err)
	removed := newCodec(t, current, nil, now)
	_, err = removed.Decode(oldToken, expected)
	assert.ErrorIs(t, err, ErrInvalid)

	unknown := testKey("unknown", "c", now.Add(time.Hour))
	_, err = rotated.Decode(signedToken(unknown, mustPayloadJSON(t, unknown, expected)), expected)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestCodecExpiresAtTheFixedSigningKeyDeadline(t *testing.T) {
	now := testTime()
	key := testKey("current", "a", now.Add(time.Hour))
	codec := newCodec(t, key, nil, now)
	expected := testPayload("release:1.0.0")
	token, err := codec.Encode(expected)
	require.NoError(t, err)

	codec.now = func() time.Time { return key.Deadline }
	_, err = codec.Decode(token, expected)
	assert.ErrorIs(t, err, ErrExpired)
}

func TestCodecValidatesKeysAndTokenSize(t *testing.T) {
	now := testTime()
	valid := testKey("current", "a", now.Add(time.Hour))
	_, err := New(Key{ID: "current", Secret: []byte("short"), Deadline: valid.Deadline}, nil)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = New(Key{Secret: valid.Secret, Deadline: valid.Deadline}, nil)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = New(Key{ID: "current", Secret: valid.Secret}, nil)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = New(valid, []Key{valid})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = New(valid, []Key{testKey("one", "b", valid.Deadline), testKey("two", "c", valid.Deadline), testKey("three", "d", valid.Deadline), testKey("four", "e", valid.Deadline)})
	assert.ErrorIs(t, err, ErrInvalid)

	codec := newCodec(t, valid, nil, now)
	oversized := testPayload("release:1.0.0")
	oversized.Last = strings.Repeat("x", maxTokenBytes)
	_, err = codec.Encode(oversized)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestCodecIsSafeForConcurrentReads(t *testing.T) {
	now := testTime()
	codec := newCodec(t, testKey("current", "a", now.Add(time.Hour)), nil, now)
	expected := testPayload("release:1.0.0")
	token, err := codec.Encode(expected)
	require.NoError(t, err)

	var group sync.WaitGroup
	errorsSeen := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := codec.Decode(token, expected)
			if err != nil {
				errorsSeen <- err
			} else if value.Snapshot != expected.Snapshot {
				errorsSeen <- errors.New("decoded snapshot changed")
			}
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		require.NoError(t, err)
	}
}

func testTime() time.Time {
	return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
}

func testKey(id, letter string, deadline time.Time) Key {
	return Key{ID: id, Secret: []byte(strings.Repeat(letter, 32)), Deadline: deadline}
}

func testPayload(snapshot string) Payload {
	return Payload{Resource: "releases", Filters: map[string]string{"kind": "schema"}, Limit: 100, Sort: "sequence", Last: "after", Snapshot: snapshot}
}

func newCodec(t *testing.T, active Key, previous []Key, now time.Time) *Codec {
	t.Helper()
	codec, err := New(active, previous)
	require.NoError(t, err)
	codec.now = func() time.Time { return now }
	return codec
}

func signedToken(key Key, data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(sign(key.Secret, data))
}

func mustPayloadJSON(t *testing.T, key Key, payload Payload) []byte {
	t.Helper()
	payload.Version = cursorVersion
	payload.KeyID = key.ID
	payload.Deadline = key.Deadline
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}
