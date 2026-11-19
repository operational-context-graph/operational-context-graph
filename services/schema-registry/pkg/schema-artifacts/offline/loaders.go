package offline

import (
	"errors"
	"fmt"

	"github.com/piprate/json-gold/ld"
	"github.com/santhosh-tekuri/jsonschema/v6"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// errExternalReference marks a document lookup outside the caller-supplied catalog.
var errExternalReference = errors.New("external document references are disabled")

// JSONSchemaLoader rejects every document lookup without accessing external resources.
type JSONSchemaLoader struct{}

// Load rejects url with an error and never retrieves a document.
// It implements [jsonschema.URLLoader].
func (JSONSchemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("load JSON Schema reference %q: %w", url, errExternalReference)
}

// CatalogJSONSchemaLoader resolves only caller-supplied release-catalog entries.
//
// It never falls back to filesystem, environment, or network lookup.
// Values MUST be decoded JSON documents; returned documents share the catalog's data.
// A nil or empty catalog rejects every lookup.
type CatalogJSONSchemaLoader map[string]any

// Load returns the document stored under the exact url key, or an error when that key is absent.
// It implements jsonschema.URLLoader and does not copy or validate the returned document.
func (loader CatalogJSONSchemaLoader) Load(url string) (any, error) {
	document, ok := loader[url]
	if !ok {
		return nil, fmt.Errorf("load JSON Schema reference %q: %w", url, errExternalReference)
	}
	return document, nil
}

// JSONLDLoader rejects every context lookup without accessing external resources.
type JSONLDLoader struct{}

// LoadDocument rejects url with an error and never retrieves a document.
// It implements [ld.DocumentLoader].
func (JSONLDLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	return nil, fmt.Errorf("load JSON-LD reference %q: %w", url, errExternalReference)
}

// CatalogJSONLDLoader resolves only caller-supplied release-catalog entries.
// Values MUST be decoded JSON documents; returned documents share the catalog's data.
// A nil or empty catalog rejects every lookup without filesystem or network fallback.
type CatalogJSONLDLoader map[string]any

// LoadDocument wraps the document stored under the exact url key, or returns an error when that key is absent.
// It implements [ld.DocumentLoader], sets DocumentURL to url, and does not copy or validate the document.
func (loader CatalogJSONLDLoader) LoadDocument(url string) (*ld.RemoteDocument, error) {
	document, ok := loader[url]
	if !ok {
		return nil, fmt.Errorf("load JSON-LD reference %q: %w", url, errExternalReference)
	}
	return &ld.RemoteDocument{DocumentURL: url, Document: document}, nil
}

// NewJSONSchemaCompiler returns a JSON Schema compiler that rejects external document lookups.
// Callers can register local resources with the compiler; built-in schemas remain available.
func NewJSONSchemaCompiler() *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(JSONSchemaLoader{})
	return compiler
}

// NewCatalogJSONSchemaCompiler returns a compiler restricted to catalog entries.
//
// The catalog maps exact document identifiers to decoded JSON values; nil rejects all loader lookups.
// The compiler also supports locally registered resources and built-in schemas.
// It retains catalog without copying; callers MUST keep the map and its documents stable during use.
func NewCatalogJSONSchemaCompiler(catalog map[string]any) *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(CatalogJSONSchemaLoader(catalog))
	return compiler
}

// ValidateInputSize rejects input larger than the version 1 artifact byte limit.
// It accepts empty input and does not check encoding, syntax, or content.
func ValidateInputSize(value []byte) error {
	if len(value) > schemaartifacts.MaxArtifactBytes {
		return fmt.Errorf("input exceeds %d byte limit", schemaartifacts.MaxArtifactBytes)
	}
	return nil
}
