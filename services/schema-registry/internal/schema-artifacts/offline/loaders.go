// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package offline supplies explicit deny-by-default document loaders.
package offline

import (
	"errors"
	"fmt"

	"github.com/piprate/json-gold/ld"
	"github.com/santhosh-tekuri/jsonschema/v6"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

var errExternalReference = errors.New("external document references are disabled")

// JSONSchemaLoader refuses every URL. Phase two adds a release-catalog loader.
type JSONSchemaLoader struct{}

// Load implements jsonschema.URLLoader without accessing external resources.
func (JSONSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("load JSON Schema reference %q: %w", url, errExternalReference)
}

// CatalogJSONSchemaLoader resolves only caller-supplied release-catalog entries.
// It never falls back to filesystem, environment, or network lookup.
type CatalogJSONSchemaLoader map[string]any

// Load implements jsonschema.URLLoader using a fixed in-memory catalog.
func (loader CatalogJSONSchemaLoader) Load(url string) (any, error) {
	document, ok := loader[url]
	if !ok {
		return nil, fmt.Errorf("load JSON Schema reference %q: %w", url, errExternalReference)
	}
	return document, nil
}

// JSONLDLoader refuses every URL. Phase two adds a release-catalog loader.
type JSONLDLoader struct{}

// LoadDocument implements ld.DocumentLoader without accessing external resources.
func (JSONLDLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	return nil, fmt.Errorf("load JSON-LD reference %q: %w", url, errExternalReference)
}

// CatalogJSONLDLoader resolves only caller-supplied release-catalog entries.
type CatalogJSONLDLoader map[string]any

// LoadDocument implements ld.DocumentLoader using a fixed in-memory catalog.
func (loader CatalogJSONLDLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	document, ok := loader[url]
	if !ok {
		return nil, fmt.Errorf("load JSON-LD reference %q: %w", url, errExternalReference)
	}
	return &ld.RemoteDocument{DocumentURL: url, Document: document}, nil
}

// NewJSONSchemaCompiler returns a compiler that cannot fetch remote references.
func NewJSONSchemaCompiler() *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(JSONSchemaLoader{})
	return compiler
}

// NewCatalogJSONSchemaCompiler returns a compiler restricted to catalog entries.
func NewCatalogJSONSchemaCompiler(catalog map[string]any) *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(CatalogJSONSchemaLoader(catalog))
	return compiler
}

// ValidateInputSize rejects bytes that future parser phases must not accept.
func ValidateInputSize(value []byte) error {
	if len(value) > config.MaxArtifactBytes {
		return fmt.Errorf("input exceeds %d byte limit", config.MaxArtifactBytes)
	}
	return nil
}
