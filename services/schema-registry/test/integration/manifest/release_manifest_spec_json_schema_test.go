package manifest_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
)

const manifestSchemaURL = "https://example.test/schema-release-manifest-v1.0.0.json"

func loadManifestJSONSchema(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../../docs/specifications/schema-release-manifest-v1.0.0.json")
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	require.NoError(t, compiler.AddResource(manifestSchemaURL, document))
	return compiler
}

func compileManifestJSONSchema(t *testing.T, compiler *jsonschema.Compiler, definition string) *jsonschema.Schema {
	t.Helper()
	location := manifestSchemaURL
	if definition != "" {
		location += "#/$defs/" + definition
	}
	schema, err := compiler.Compile(location)
	require.NoError(t, err)
	return schema
}

// TestManifestJSONSchemaValidFixtures checks existing manifest fixtures against the version 1.0.0 JSON Schema.
// It validates parsed structure, not canonical bytes, artifact content, or release history.
func TestManifestJSONSchemaValidFixtures(t *testing.T) {
	compiler := loadManifestJSONSchema(t)
	schema := compileManifestJSONSchema(t, compiler, "")
	tests := []struct {
		name string
		file string
	}{
		{name: "specification example", file: "examples/release-manifest-spec-example.json"},
		{name: "initial release", file: "valid-initial.json"},
		{name: "initial release with extension", file: "valid-initial-with-extension.json"},
		{name: "example initial release", file: "examples/initial.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(corpus.ReadFixture(t, test.file)))
			require.NoError(t, err)
			require.NoError(t, schema.Validate(document))
		})
	}
}
