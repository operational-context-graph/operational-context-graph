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

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// maxSafeInteger bounds the contiguous interoperable integer range of IEEE 754 binary64.
const maxSafeInteger = 9007199254740991

var (
	// ErrTooLarge identifies a document that exceeds schemaartifacts.MaxArtifactBytes.
	ErrTooLarge = errors.New("JSON input exceeds the byte limit")
	// ErrMalformed identifies invalid syntax, encoding, or an unexpected JSON value shape.
	ErrMalformed = errors.New("malformed JSON")
	// ErrDuplicate identifies repeated member names within one JSON object.
	ErrDuplicate = errors.New("duplicate JSON object member")
	// ErrDepth identifies a value traversal that exceeds schemaartifacts.MaxJSONDepth.
	ErrDepth = errors.New("JSON nesting exceeds the limit")
	// ErrObjectMembers identifies an object that exceeds schemaartifacts.MaxObjectMembers.
	ErrObjectMembers = errors.New("JSON object has too many members")
	// ErrArrayEntries identifies an array that exceeds schemaartifacts.MaxArrayEntries.
	ErrArrayEntries = errors.New("JSON array has too many entries")
	// ErrStringTooLarge identifies a string or member name that exceeds schemaartifacts.MaxJSONStringBytes.
	ErrStringTooLarge = errors.New("JSON string exceeds the byte limit")
	// ErrNumber identifies a number outside the supported finite, interoperable numeric range.
	ErrNumber = errors.New("value is not an I-JSON number")
)

// Parse decodes one complete bounded I-JSON document.
//
// Objects use map values, arrays use []any, and numbers retain [json.Number] for exact interpretation.
// Strings, booleans, and null become string, bool, and nil values.
//
// It accepts surrounding JSON whitespace but rejects a byte order mark and additional top-level values.
// Integral numbers MUST lie within the contiguous binary64 safe integer range, even when written with exponents.
//
// Errors match the applicable package sentinel with [errors.Is].
func Parse(input []byte) (any, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformed)
	}
	if len(input) > schemaartifacts.MaxArtifactBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(input) || bytes.HasPrefix(input, []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("%w: invalid UTF-8 encoding", ErrMalformed)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	// Preserve numeric spelling so range checks run before any rounding to binary64 can hide an unsafe integer.
	decoder.UseNumber()
	value, err := parseValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing value", ErrMalformed)
		}
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return value, nil
}

// Object parses a document that must contain a JSON object.
// It applies [Parse]'s limits and returns an error wrapping [ErrMalformed] for any other top-level value.
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

// RejectUnknownFields rejects object members whose names do not appear in allowed.
//
// It returns an error wrapping [ErrMalformed] for an unknown member.
// It does not require allowed members to exist or check their values; a nil object is accepted.
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

// parseValue reads one JSON value and enforces depth, string, and numeric limits.
func parseValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > schemaartifacts.MaxJSONDepth {
		return nil, ErrDepth
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
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
		if len(value) > schemaartifacts.MaxJSONStringBytes {
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

// parseObject reads members after an opening brace, rejecting duplicates and oversized names or objects.
func parseObject(decoder *json.Decoder, depth int) (map[string]any, error) {
	object := make(map[string]any)
	for decoder.More() {
		if len(object) == schemaartifacts.MaxObjectMembers {
			return nil, ErrObjectMembers
		}
		key, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("%w: object member name", ErrMalformed)
		}
		if len(name) > schemaartifacts.MaxJSONStringBytes {
			return nil, ErrStringTooLarge
		}
		// Check decoded names, so escaped and literal spellings of the same member cannot bypass duplicate detection.
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

// parseArray reads elements after an opening bracket and enforces the array entry limit.
func parseArray(decoder *json.Decoder, depth int) ([]any, error) {
	array := make([]any, 0)
	for decoder.More() {
		if len(array) == schemaartifacts.MaxArrayEntries {
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

// validateNumber requires finite binary64 conversion and restricts integral values to the exactly representable range.
func validateNumber(number json.Number) error {
	// Rational parsing detects integral exponent and decimal spellings without losing precision through float conversion.
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
