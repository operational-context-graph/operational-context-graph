// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package offline

import (
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
)

// TestLoadersRejectExternalReferences checks that deny-all loaders reject network and filesystem document identifiers.
func TestLoadersRejectExternalReferences(t *testing.T) {
	for _, uri := range []string{"https://example.test/document", "file:///unavailable/document", "urn:example:document"} {
		t.Run(uri, func(t *testing.T) {
			value, err := (JSONSchemaLoader{}).Load(uri)
			require.ErrorIs(t, err, errExternalReference)
			assert.Nil(t, value)
			document, err := (JSONLDLoader{}).LoadDocument(uri)
			require.ErrorIs(t, err, errExternalReference)
			assert.Nil(t, document)
		})
	}
}

// TestValidateInputSize verifies acceptance at the byte limit and rejection immediately above it.
func TestValidateInputSize(t *testing.T) {
	for _, size := range []int{0, schemaartifacts.MaxArtifactBytes, schemaartifacts.MaxArtifactBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			err := ValidateInputSize(make([]byte, size))
			if size > schemaartifacts.MaxArtifactBytes {
				require.ErrorContains(t, err, "byte limit")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestCatalogLoadersResolveOnlyCatalogEntries verifies exact in-memory lookups and rejection of uncataloged schema identifiers.
func TestCatalogLoadersResolveOnlyCatalogEntries(t *testing.T) {
	value := map[string]any{"type": "object"}
	schemaLoader := CatalogJSONSchemaLoader{"https://example.test/schema": value}
	loaded, err := schemaLoader.Load("https://example.test/schema")
	require.NoError(t, err)
	require.Equal(t, value, loaded)
	_, err = schemaLoader.Load("https://example.test/other")
	require.ErrorIs(t, err, errExternalReference)

	ldLoader := CatalogJSONLDLoader{"https://example.test/context": value}
	document, err := ldLoader.LoadDocument("https://example.test/context")
	require.NoError(t, err)
	require.Equal(t, value, document.Document)
	assert.Equal(t, "https://example.test/context", document.DocumentURL)
	assert.Empty(t, document.ContextURL)

	for _, uri := range []string{"https://example.test/other", "https://example.test/Context", "https://example.test/context/"} {
		document, loadErr := ldLoader.LoadDocument(uri)
		require.ErrorIs(t, loadErr, errExternalReference)
		assert.Nil(t, document)
		value, loadErr := schemaLoader.Load(uri)
		require.ErrorIs(t, loadErr, errExternalReference)
		assert.Nil(t, value)
	}
	_, err = (CatalogJSONSchemaLoader(nil)).Load("https://example.test/schema")
	require.ErrorIs(t, err, errExternalReference)
	_, err = (CatalogJSONLDLoader(nil)).LoadDocument("https://example.test/context")
	require.ErrorIs(t, err, errExternalReference)
}

// TestJSONSchemaCompilers checks local references and catalog-only external resolution through the compiler API.
func TestJSONSchemaCompilers(t *testing.T) {
	const uri = "https://example.test/schema"
	for _, test := range []struct {
		name     string
		compiler func() *jsonschema.Compiler
	}{
		{"deny all", NewJSONSchemaCompiler},
		{"nil catalog", func() *jsonschema.Compiler { return NewCatalogJSONSchemaCompiler(nil) }},
		{"catalog", func() *jsonschema.Compiler {
			return NewCatalogJSONSchemaCompiler(map[string]any{"https://example.test/string": map[string]any{"type": "string"}})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiler := test.compiler()
			require.NoError(t, compiler.AddResource(uri, map[string]any{
				"$defs": map[string]any{"string": map[string]any{"type": "string"}}, "$ref": "#/$defs/string",
			}))
			schema, err := compiler.Compile(uri)
			require.NoError(t, err)
			assert.NoError(t, schema.Validate("value"))
			assert.Error(t, schema.Validate(42))

			compiler = test.compiler()
			require.NoError(t, compiler.AddResource(uri, map[string]any{"$ref": "https://example.test/string"}))
			schema, err = compiler.Compile(uri)
			if test.name == "catalog" {
				require.NoError(t, err)
				assert.NoError(t, schema.Validate("value"))
				assert.Error(t, schema.Validate(false))
			} else {
				var loadError *jsonschema.LoadURLError
				require.ErrorAs(t, err, &loadError)
				assert.Equal(t, "https://example.test/string", loadError.URL)
				require.ErrorIs(t, loadError.Err, errExternalReference)
			}
			_, err = compiler.Compile("https://example.test/missing")
			var loadError *jsonschema.LoadURLError
			require.ErrorAs(t, err, &loadError)
			assert.Equal(t, "https://example.test/missing", loadError.URL)
			require.ErrorIs(t, loadError.Err, errExternalReference)
		})
	}
}
