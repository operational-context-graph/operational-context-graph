// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/api/v1"
	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

var (
	// ErrTooLarge identifies an identity representation exceeding the API limit.
	ErrTooLarge = errors.New("API representation exceeds response limit")
	// ErrInvalid identifies content that cannot produce the shared JSON representation.
	ErrInvalid = errors.New("invalid API representation")
)

// MappingPackageSize counts the exact identity JSON bytes without encoding program content.
// Callers MUST first validate the release, bindings, and declared program sizes.
func MappingPackageSize(value model.MappingPackage, version string) (int64, error) {
	body, err := mappingPackageMetadata(value, version)
	if err != nil {
		return 0, err
	}
	return mappingPackageSize(value, body, config.MaxGeneratedJSONBytes)
}

func mappingPackageSize(value model.MappingPackage, metadata v1.MappingPackageResponse, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("%w: negative size limit", ErrInvalid)
	}
	var size int64
	add := func(amount int64) error {
		if amount < 0 {
			return fmt.Errorf("%w: invalid JSON size", ErrInvalid)
		}
		if amount > limit-size {
			return ErrTooLarge
		}
		size += amount
		return nil
	}
	for _, program := range value.Programs {
		if program.Size < 0 {
			return 0, fmt.Errorf("%w: negative program size", ErrInvalid)
		}
		// Check before converting to int or calculating padded Base64 length.
		if program.Size > config.MaxGeneratedJSONBytes || program.Size > limit {
			return 0, ErrTooLarge
		}
		if err := add(int64(base64.StdEncoding.EncodedLen(int(program.Size)))); err != nil {
			return 0, err
		}
	}
	for _, schema := range value.Schemas {
		if !json.Valid(schema.Artifact.Payload.Bytes) {
			return 0, fmt.Errorf("%w: invalid schema JSON", ErrInvalid)
		}
		encoded, err := json.Marshal(json.RawMessage(schema.Artifact.Payload.Bytes))
		if err != nil {
			return 0, fmt.Errorf("%w: encode schema JSON: %w", ErrInvalid, err)
		}
		if err := add(int64(len(encoded))); err != nil {
			return 0, err
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return 0, fmt.Errorf("%w: encode package metadata: %w", ErrInvalid, err)
	}
	// The shared metadata model includes one four-byte null document placeholder per schema.
	// Empty program content already includes its quotes; Base64 needs no JSON escaping.
	if err := add(int64(len(encoded)) - int64(len(metadata.Schemas))*4); err != nil {
		return 0, err
	}
	return size, nil
}
