// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package pagination implements signed, stateless keyset cursors.
package pagination

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
)

const (
	maxTokenBytes   = 8 * 1024
	maxPreviousKeys = 3
	cursorVersion   = 1
)

var (
	ErrInvalid = errors.New("invalid cursor")
	ErrExpired = errors.New("cursor expired")
)

// Key is one non-secret cursor-signing key identifier and its secret material.
type Key struct {
	ID       string
	Secret   []byte
	Deadline time.Time
}

// Payload contains the complete signed keyset state.
type Payload struct {
	Version  int               `json:"v"`
	KeyID    string            `json:"k"`
	Resource string            `json:"r"`
	Filters  map[string]string `json:"f"`
	Limit    int               `json:"l"`
	Sort     string            `json:"s"`
	Last     string            `json:"a"`
	Snapshot string            `json:"n"`
	Deadline time.Time         `json:"d"`
}

// Codec is immutable after construction and safe for concurrent Encode and
// Decode calls. Tests can replace now before starting concurrent callers.
type Codec struct {
	active Key
	keys   map[string]Key
	now    func() time.Time
}

// New creates a codec with one active key and at most three previous keys.
func New(active Key, previous []Key) (*Codec, error) {
	if !validKey(active) || len(previous) > maxPreviousKeys {
		return nil, ErrInvalid
	}
	active = cloneKey(active)
	keys := map[string]Key{active.ID: active}
	for _, key := range previous {
		if !validKey(key) {
			return nil, ErrInvalid
		}
		if _, found := keys[key.ID]; found {
			return nil, ErrInvalid
		}
		key = cloneKey(key)
		keys[key.ID] = key
	}
	return &Codec{active: active, keys: keys, now: time.Now}, nil
}

// Encode creates a canonical, HMAC-SHA-256 signed cursor with the active key.
func (c *Codec) Encode(value Payload) (string, error) {
	if c == nil || !validPayloadInput(value) {
		return "", ErrInvalid
	}
	value.Version = cursorVersion
	value.KeyID = c.active.ID
	value.Filters = cloneFilters(value.Filters)
	value.Deadline = c.active.Deadline
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxTokenBytes {
		return "", ErrInvalid
	}
	signature := sign(c.active.Secret, data)
	token := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(token) > maxTokenBytes {
		return "", ErrInvalid
	}
	return token, nil
}

// Decode verifies and binds a cursor to the request's effective parameters.
func (c *Codec) Decode(token string, expected Payload) (Payload, error) {
	if c == nil || len(token) == 0 || len(token) > maxTokenBytes || strings.Count(token, ".") != 1 || !validExpected(expected) {
		return Payload{}, ErrInvalid
	}
	parts := strings.SplitN(token, ".", 2)
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(data) == 0 || len(data) > maxTokenBytes {
		return Payload{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return Payload{}, ErrInvalid
	}
	value, err := parsePayload(data)
	if err != nil || value.Version != cursorVersion {
		return Payload{}, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(data, canonical) {
		return Payload{}, ErrInvalid
	}
	key, found := c.keys[value.KeyID]
	if !found || !hmac.Equal(sign(key.Secret, data), signature) {
		return Payload{}, ErrInvalid
	}
	if !value.Deadline.Equal(key.Deadline) {
		return Payload{}, ErrInvalid
	}
	if !c.now().Before(value.Deadline) {
		return Payload{}, ErrExpired
	}
	if value.Resource != expected.Resource || value.Limit != expected.Limit || value.Sort != expected.Sort || !sameFilters(value.Filters, expected.Filters) {
		return Payload{}, ErrInvalid
	}
	value.Filters = cloneFilters(value.Filters)
	return value, nil
}

func validKey(key Key) bool {
	return key.ID != "" && len(key.Secret) >= sha256.Size && !key.Deadline.IsZero()
}

func cloneKey(key Key) Key {
	key.Secret = append([]byte(nil), key.Secret...)
	key.Deadline = key.Deadline.UTC()
	return key
}

func validPayloadInput(value Payload) bool {
	return validExpected(value) && value.Last != "" && value.Snapshot != ""
}

func validExpected(value Payload) bool {
	return value.Resource != "" && value.Limit >= 1 && value.Limit <= 1000 && value.Sort != ""
}

func parsePayload(data []byte) (Payload, error) {
	object, err := strictjson.Object(data)
	if err != nil || strictjson.RejectUnknownFields(object, "v", "k", "r", "f", "l", "s", "a", "n", "d") != nil || len(object) != 9 {
		return Payload{}, ErrInvalid
	}
	version, ok := integer(object["v"])
	if !ok {
		return Payload{}, ErrInvalid
	}
	limit, ok := integer(object["l"])
	if !ok {
		return Payload{}, ErrInvalid
	}
	keyID, ok := object["k"].(string)
	if !ok || keyID == "" {
		return Payload{}, ErrInvalid
	}
	resource, ok := object["r"].(string)
	if !ok || resource == "" {
		return Payload{}, ErrInvalid
	}
	sort, ok := object["s"].(string)
	if !ok || sort == "" {
		return Payload{}, ErrInvalid
	}
	last, ok := object["a"].(string)
	if !ok || last == "" {
		return Payload{}, ErrInvalid
	}
	snapshot, ok := object["n"].(string)
	if !ok || snapshot == "" {
		return Payload{}, ErrInvalid
	}
	deadlineText, ok := object["d"].(string)
	if !ok {
		return Payload{}, ErrInvalid
	}
	deadline, err := time.Parse(time.RFC3339Nano, deadlineText)
	if err != nil {
		return Payload{}, ErrInvalid
	}
	filters, ok := stringMap(object["f"])
	if !ok {
		return Payload{}, ErrInvalid
	}
	value := Payload{Version: version, KeyID: keyID, Resource: resource, Filters: filters, Limit: limit, Sort: sort, Last: last, Snapshot: snapshot, Deadline: deadline.UTC()}
	if !validPayloadInput(value) {
		return Payload{}, ErrInvalid
	}
	return value, nil
}

func integer(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.Atoi(number.String())
	return parsed, err == nil
}

func stringMap(value any) (map[string]string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	filters := make(map[string]string, len(object))
	for key, entry := range object {
		text, ok := entry.(string)
		if !ok {
			return nil, false
		}
		filters[key] = text
	}
	return filters, true
}

func sign(secret, data []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("ocg-schema-registry-cursor-v1\x00"))
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func cloneFilters(filters map[string]string) map[string]string {
	clone := make(map[string]string, len(filters))
	for key, value := range filters {
		clone[key] = value
	}
	return clone
}

func sameFilters(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}
