// Package offline supplies explicit deny-by-default document loaders.
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

// JSONLDLoader rejects every context lookup without accessing external resources.
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

// ValidateInputSize rejects input larger than the version 1 artifact byte limit.
func ValidateInputSize(value []byte) error {
	if len(value) > schemaartifacts.MaxArtifactBytes {
		return fmt.Errorf("input exceeds %d byte limit", schemaartifacts.MaxArtifactBytes)
	}
	return nil
}
