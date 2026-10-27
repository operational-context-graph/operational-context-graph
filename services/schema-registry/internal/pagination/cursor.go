// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package pagination provides unsigned stateless keyset cursors.
//
// Pagination cursors are opaque, unsigned, unpadded Base64url-encoded JSON. They do not expire and
// have no signing-key or rotation configuration. The service validates every decoded value, request
// binding, and activated snapshot before use. A valid caller-constructed cursor receives the same
// visibility and service limits as a server-issued cursor. Tokens provide no cryptographic tamper
// detection.
package pagination

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"strconv"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
)

const (
	maxTokenBytes = 8 * 1024
	cursorVersion = 1
)

var ErrInvalid = errors.New("invalid cursor")

// Payload contains complete untrusted keyset state. The calling service MUST
// validate its snapshot against visible read-model data before using it.
type Payload struct {
	Version  int               `json:"v"`
	Resource string            `json:"r"`
	Filters  map[string]string `json:"f"`
	Limit    int               `json:"l"`
	Sort     string            `json:"s"`
	Last     string            `json:"a"`
	Snapshot string            `json:"n"`
}

// Codec is safe for concurrent Encode and Decode calls.
type Codec struct{}

// New creates an unsigned cursor codec.
func New() *Codec { return &Codec{} }

// Encode creates a canonical versioned JSON cursor with unpadded Base64url.
func (c *Codec) Encode(value Payload) (string, error) {
	if c == nil || !validPayloadInput(value) {
		return "", ErrInvalid
	}
	value.Version = cursorVersion
	value.Filters = cloneFilters(value.Filters)
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxTokenBytes {
		return "", ErrInvalid
	}
	token := base64.RawURLEncoding.EncodeToString(data)
	if len(token) > maxTokenBytes {
		return "", ErrInvalid
	}
	return token, nil
}

// Decode validates a cursor and binds it to effective request parameters.
func (c *Codec) Decode(token string, expected Payload) (Payload, error) {
	if c == nil || token == "" || len(token) > maxTokenBytes || !validExpected(expected) {
		return Payload{}, ErrInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) == 0 || len(data) > maxTokenBytes {
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
	if value.Resource != expected.Resource || value.Limit != expected.Limit || value.Sort != expected.Sort || !sameFilters(value.Filters, expected.Filters) {
		return Payload{}, ErrInvalid
	}
	value.Filters = cloneFilters(value.Filters)
	return value, nil
}

func validPayloadInput(value Payload) bool {
	return validExpected(value) && value.Last != "" && value.Snapshot != ""
}

func validExpected(value Payload) bool {
	return value.Resource != "" && value.Limit >= 1 && value.Limit <= 1000 && value.Sort != ""
}

func parsePayload(data []byte) (Payload, error) {
	object, err := strictObject(data)
	if err != nil || len(object) != 7 {
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
	filters, ok := stringMap(object["f"])
	if !ok {
		return Payload{}, ErrInvalid
	}
	value := Payload{Version: version, Resource: resource, Filters: filters, Limit: limit, Sort: sort, Last: last, Snapshot: snapshot}
	if !validPayloadInput(value) {
		return Payload{}, ErrInvalid
	}
	return value, nil
}

func strictObject(data []byte) (map[string]any, error) {
	object, err := strictjson.Object(data)
	if err != nil || strictjson.RejectUnknownFields(object, "v", "r", "f", "l", "s", "a", "n") != nil {
		return nil, ErrInvalid
	}
	return object, nil
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

func cloneFilters(filters map[string]string) map[string]string {
	clone := make(map[string]string, len(filters))
	maps.Copy(clone, filters)
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
