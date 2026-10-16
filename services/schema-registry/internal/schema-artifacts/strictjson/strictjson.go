// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package strictjson parses bounded RFC 7493 I-JSON documents.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"unicode/utf8"
)

const (
	MaxBytes         = 16 * 1024 * 1024
	MaxDepth         = 64
	MaxObjectMembers = 256
	MaxArrayEntries  = 10000
	MaxStringBytes   = 1024 * 1024
	maxSafeInteger   = 9007199254740991
)

var (
	ErrTooLarge       = errors.New("JSON input exceeds the byte limit")
	ErrMalformed      = errors.New("malformed JSON")
	ErrDuplicate      = errors.New("duplicate JSON object member")
	ErrDepth          = errors.New("JSON nesting exceeds the limit")
	ErrObjectMembers  = errors.New("JSON object has too many members")
	ErrArrayEntries   = errors.New("JSON array has too many entries")
	ErrStringTooLarge = errors.New("JSON string exceeds the byte limit")
	ErrNumber         = errors.New("value is not an I-JSON number")
)

// Parse decodes one complete bounded I-JSON document. Objects use map values,
// arrays use []any, and numbers retain json.Number for exact interpretation.
func Parse(input []byte) (any, error) {
	if input == nil || len(input) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformed)
	}
	if len(input) > MaxBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(input) || bytes.HasPrefix(input, []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("%w: invalid UTF-8 encoding", ErrMalformed)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := parseValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing value", ErrMalformed)
		}
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return value, nil
}

// Object parses a document that must contain a JSON object.
func Object(input []byte) (map[string]any, error) {
	value, err := Parse(input)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected object", ErrMalformed)
	}
	return object, nil
}

// RejectUnknownFields verifies the declared object shape.
func RejectUnknownFields(object map[string]any, allowed ...string) error {
	permitted := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		permitted[field] = struct{}{}
	}
	for field := range object {
		if _, ok := permitted[field]; !ok {
			return fmt.Errorf("%w: %q", ErrMalformed, field)
		}
	}
	return nil
}

func parseValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, ErrDepth
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return parseObject(decoder, depth+1)
		case '[':
			return parseArray(decoder, depth+1)
		default:
			return nil, fmt.Errorf("%w: unexpected delimiter", ErrMalformed)
		}
	case string:
		if len(value) > MaxStringBytes {
			return nil, ErrStringTooLarge
		}
		return value, nil
	case json.Number:
		if err := validateNumber(value); err != nil {
			return nil, err
		}
		return value, nil
	case bool, nil:
		return value, nil
	default:
		return nil, fmt.Errorf("%w: unsupported token", ErrMalformed)
	}
}

func parseObject(decoder *json.Decoder, depth int) (map[string]any, error) {
	object := make(map[string]any)
	for decoder.More() {
		if len(object) == MaxObjectMembers {
			return nil, ErrObjectMembers
		}
		key, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("%w: object member name", ErrMalformed)
		}
		if len(name) > MaxStringBytes {
			return nil, ErrStringTooLarge
		}
		if _, exists := object[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicate, name)
		}
		value, err := parseValue(decoder, depth)
		if err != nil {
			return nil, err
		}
		object[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("%w: object closing delimiter", ErrMalformed)
	}
	return object, nil
}

func parseArray(decoder *json.Decoder, depth int) ([]any, error) {
	array := make([]any, 0)
	for decoder.More() {
		if len(array) == MaxArrayEntries {
			return nil, ErrArrayEntries
		}
		value, err := parseValue(decoder, depth)
		if err != nil {
			return nil, err
		}
		array = append(array, value)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, fmt.Errorf("%w: array closing delimiter", ErrMalformed)
	}
	return array, nil
}

func validateNumber(number json.Number) error {
	rational, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return fmt.Errorf("%w: %q", ErrNumber, number)
	}
	if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
		return fmt.Errorf("%w: %q", ErrNumber, number)
	}
	if rational.IsInt() {
		integer := rational.Num()
		if integer.Cmp(big.NewInt(maxSafeInteger)) > 0 || integer.Cmp(big.NewInt(-maxSafeInteger)) < 0 {
			return fmt.Errorf("%w: integer %q exceeds exact range", ErrNumber, number)
		}
	}
	return nil
}
