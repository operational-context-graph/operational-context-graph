// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/gowebpki/jcs"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// Canonicalize sorts format-defined manifest arrays and returns RFC 8785 UTF-8 bytes.
// It rejects duplicate members, invalid structure, and profile violations without repairing or deduplicating entries.
// It preserves extension arrays and never loads or reserializes artifact payloads, including ordered mapping transformations.
// Callers MUST separately validate the release closure and application response serviceability.
func Canonicalize(data []byte) ([]byte, error) {
	document, err := strictjson.Object(data)
	if err != nil {
		return nil, fmt.Errorf("canonicalize manifest: %w", err)
	}
	// encoding/json replaces lone UTF-16 surrogates. Reject them before re-encoding decoded strings.
	if _, canonicalErr := jcs.Transform(data); canonicalErr != nil {
		return nil, fmt.Errorf("%w: canonicalize manifest: %w", ErrInvalid, canonicalErr)
	}
	sortObjects(document, "artifacts", "kind", "artifactType")
	artifacts, _ := document["artifacts"].([]any)
	for _, value := range artifacts {
		item, _ := value.(map[string]any)
		sortObjects(item, "resources", "role", "name")
		sortObjects(item, "dependencies", "kind", "artifactType", "artifactRevision")
	}
	sortObjects(document, "schemaContextBindings", "schemaRevision")
	bindings, _ := document["schemaContextBindings"].([]any)
	for _, value := range bindings {
		item, _ := value.(map[string]any)
		sortStrings(item, "contextRevisions")
	}
	sortObjects(document, "uriCatalog", "uri")
	sortObjects(document, "storageCatalog", "kind", "artifactType")
	storage, _ := document["storageCatalog"].([]any)
	for _, value := range storage {
		item, _ := value.(map[string]any)
		sortStrings(item, "stores")
	}
	if changes, ok := document["changes"].(map[string]any); ok {
		sortObjects(changes, "artifacts", "kind", "artifactType", "operation")
		sortObjects(changes, "schemaContextBindings", "schemaRevision", "contextRevision", "operation")
		sortObjects(changes, "uriCatalog", "uri", "operation")
		sortObjects(changes, "storageCatalog", "kind", "artifactType", "store", "operation")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, fmt.Errorf("canonicalize manifest: %w", err)
	}
	if _, err := Parse(canonical); err != nil {
		return nil, err
	}
	return canonical, nil
}

// sortObjects only orders decoded arrays. Parse subsequently rejects invalid element shapes and duplicate sort keys.
func sortObjects(object map[string]any, field string, keys ...string) {
	values, _ := object[field].([]any)
	slices.SortStableFunc(values, func(left, right any) int {
		a, _ := left.(map[string]any)
		b, _ := right.(map[string]any)
		for _, key := range keys {
			x, _ := a[key].(string)
			y, _ := b[key].(string)
			if order := compareUTF16(x, y); order != 0 {
				return order
			}
		}
		return 0
	})
}

// sortStrings preserves string values, including their Unicode normalization forms.
func sortStrings(object map[string]any, field string) {
	values, _ := object[field].([]any)
	slices.SortStableFunc(values, func(left, right any) int {
		a, _ := left.(string)
		b, _ := right.(string)
		return compareUTF16(a, b)
	})
}
