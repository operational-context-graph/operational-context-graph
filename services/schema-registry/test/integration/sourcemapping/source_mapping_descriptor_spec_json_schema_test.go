package sourcemapping_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
)

const descriptorSchemaURL = "https://example.test/source-mapping-descriptor-v0.1.0.json"

func loadDescriptorJSONSchema(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../../docs/specifications/source-mapping-descriptor-v0.1.0.json")
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	require.NoError(t, compiler.AddResource(descriptorSchemaURL, document))
	return compiler
}

func compileDescriptorJSONSchema(t *testing.T, compiler *jsonschema.Compiler, definition string) *jsonschema.Schema {
	t.Helper()
	location := descriptorSchemaURL
	if definition != "" {
		location += "#/$defs/" + definition
	}
	schema, err := compiler.Compile(location)
	require.NoError(t, err)
	return schema
}

func descriptorFixture(t *testing.T) map[string]any {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(corpus.ReadFixture(t, "mapping-a.json")))
	require.NoError(t, err)
	return document.(map[string]any)
}

// descriptorObjects returns every format-defined object in the first transformation's branch.
func descriptorObjects(document map[string]any) map[string]map[string]any {
	programs := document["programs"].(map[string]any)
	transformation := programs["transformations"].([]any)[0].(map[string]any)
	return map[string]map[string]any{
		"descriptor":     document,
		"sourceSchema":   document["sourceSchema"].(map[string]any),
		"engine":         document["engine"].(map[string]any),
		"programs":       programs,
		"condition":      programs["condition"].(map[string]any),
		"transformation": transformation,
		"targetSchema":   transformation["targetSchema"].(map[string]any),
	}
}

// TestDescriptorJSONSchemaValidFixtures checks existing descriptor fixtures and the specification example against the version 0.1.0 JSON Schema.
// Every parsed document must satisfy the descriptor's structural rules with URI format assertions enabled.
// It does not validate exact bytes, resource bindings, or release history.
func TestDescriptorJSONSchemaValidFixtures(t *testing.T) {
	schema := compileDescriptorJSONSchema(t, loadDescriptorJSONSchema(t), "")
	for _, file := range []string{"mapping-a.json", "mapping-b.json", "mapping-c.json", "mapping-d.json", "mapping-e.json"} {
		t.Run(file, func(t *testing.T) {
			document, err := jsonschema.UnmarshalJSON(bytes.NewReader(corpus.ReadFixture(t, file)))
			require.NoError(t, err)
			require.NoError(t, schema.Validate(document))
		})
	}
	t.Run("specification example", func(t *testing.T) {
		specification, err := os.ReadFile("../../../docs/specifications/source-mapping-descriptor-v0.1.0.md")
		require.NoError(t, err)
		_, example, ok := strings.Cut(string(specification), "```json\n")
		require.True(t, ok)
		example, _, ok = strings.Cut(example, "```")
		require.True(t, ok)
		document, err := jsonschema.UnmarshalJSON(strings.NewReader(example))
		require.NoError(t, err)
		require.NoError(t, schema.Validate(document))
	})
}

// TestDescriptorJSONSchemaRequiredFields mutates required fields in the descriptor and every format-defined nested object.
// Validation must reject missing fields, null values, incorrect JSON types, and empty strings.
func TestDescriptorJSONSchemaRequiredFields(t *testing.T) {
	schema := compileDescriptorJSONSchema(t, loadDescriptorJSONSchema(t), "")
	required := map[string][]string{
		"descriptor":     {"$schema", "formatVersion", "id", "sourceSchema", "engine", "programs"},
		"sourceSchema":   {"id", "revision"},
		"engine":         {"language", "languageVersion", "executionProfile"},
		"programs":       {"transformations"},
		"condition":      {"programRef", "programDigest"},
		"transformation": {"programRef", "programDigest", "targetSchema", "cardinality"},
		"targetSchema":   {"id", "revision"},
	}
	for object, fields := range required {
		for _, field := range fields {
			for _, mutation := range []string{"missing", "null", "wrong type", "empty string"} {
				t.Run(object+"/"+field+"/"+mutation, func(t *testing.T) {
					document := descriptorFixture(t)
					target := descriptorObjects(document)[object]
					switch mutation {
					case "missing":
						delete(target, field)
					case "null":
						target[field] = nil
					case "wrong type":
						target[field] = true
					case "empty string":
						target[field] = ""
					}
					require.Error(t, schema.Validate(document))
				})
			}
		}
	}
}

// TestDescriptorJSONSchemaUnknownFields adds an undeclared field to the descriptor and each format-defined nested object.
// Validation must reject every unknown field, including fields in conditions, transformations, and schema references.
func TestDescriptorJSONSchemaUnknownFields(t *testing.T) {
	schema := compileDescriptorJSONSchema(t, loadDescriptorJSONSchema(t), "")
	for object := range descriptorObjects(descriptorFixture(t)) {
		t.Run(object, func(t *testing.T) {
			document := descriptorFixture(t)
			descriptorObjects(document)[object]["unknown"] = "value"
			require.Error(t, schema.Validate(document))
		})
	}
}

// TestDescriptorJSONSchemaProgramRules checks descriptor shape, supported format versions, program structure, cardinality, and metadata constraints.
// Validation must reject malformed entries and transformation-only fields in conditions.
// Publisher-declared engine identifiers and opaque resource names remain valid without registry capability or path restrictions.
// Omitting the condition and repeating identical transformations must also remain valid.
func TestDescriptorJSONSchemaProgramRules(t *testing.T) {
	schema := compileDescriptorJSONSchema(t, loadDescriptorJSONSchema(t), "")
	tests := []struct {
		name   string
		object string
		field  string
		value  any
		valid  bool
	}{
		{name: "null descriptor", object: "root", value: nil},
		{name: "array descriptor", object: "root", value: []any{}},
		{name: "unsupported major", object: "descriptor", field: "formatVersion", value: "2.0.0"},
		{name: "unsupported minor", object: "descriptor", field: "formatVersion", value: "1.1.0"},
		{name: "unsupported patch", object: "descriptor", field: "formatVersion", value: "1.0.1"},
		{name: "manifest-only field", object: "descriptor", field: "targetSchemaVersion", value: "1.0.0"},
		{name: "null condition", object: "programs", field: "condition", value: nil},
		{name: "string condition", object: "programs", field: "condition", value: "condition.bin"},
		{name: "array condition", object: "programs", field: "condition", value: []any{}},
		{name: "empty condition", object: "programs", field: "condition", value: map[string]any{}},
		{name: "condition cardinality", object: "condition", field: "cardinality", value: "one"},
		{name: "condition target", object: "condition", field: "targetSchema", value: map[string]any{"id": "entity", "revision": "r1"}},
		{name: "empty transformations", object: "programs", field: "transformations", value: []any{}},
		{name: "object transformations", object: "programs", field: "transformations", value: map[string]any{}},
		{name: "null transformation", object: "programs", field: "transformations", value: []any{nil}},
		{name: "string transformation", object: "programs", field: "transformations", value: []any{"program.bin"}},
		{name: "array transformation", object: "programs", field: "transformations", value: []any{[]any{}}},
		{name: "empty transformation", object: "programs", field: "transformations", value: []any{map[string]any{}}},
		{name: "invalid cardinality", object: "transformation", field: "cardinality", value: "zero"},
		{name: "many cardinality", object: "transformation", field: "cardinality", value: "many", valid: true},
		{name: "unregistered language", object: "engine", field: "language", value: "Publisher Language / 未知", valid: true},
		{name: "unregistered profile", object: "engine", field: "executionProfile", value: "Publisher Profile / 未知", valid: true},
		{name: "resource name is not a path", object: "condition", field: "programRef", value: "../opaque resource/未知", valid: true},
		{name: "invalid descriptor id", object: "descriptor", field: "id", value: "Invalid_ID"},
		{name: "invalid source id", object: "sourceSchema", field: "id", value: "Invalid_ID"},
		{name: "invalid target id", object: "targetSchema", field: "id", value: "Invalid_ID"},
		{name: "invalid source revision", object: "sourceSchema", field: "revision", value: ".."},
		{name: "invalid target revision", object: "targetSchema", field: "revision", value: ".."},
		{name: "invalid language version", object: "engine", field: "languageVersion", value: "01.0.0"},
		{name: "invalid condition digest", object: "condition", field: "programDigest", value: "sha256:" + strings.Repeat("a", 64)},
		{name: "invalid transformation digest", object: "transformation", field: "programDigest", value: "sha256:" + strings.Repeat("a", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := descriptorFixture(t)
			var value any = document
			if test.object == "root" {
				value = test.value
			} else {
				descriptorObjects(document)[test.object][test.field] = test.value
			}
			err := schema.Validate(value)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	t.Run("absent condition and repeated transformations", func(t *testing.T) {
		document := descriptorFixture(t)
		programs := document["programs"].(map[string]any)
		delete(programs, "condition")
		transformation := programs["transformations"].([]any)[0]
		programs["transformations"] = []any{transformation, transformation}
		require.NoError(t, schema.Validate(document))
	})
}

// TestDescriptorJSONSchemaSelectors exercises revision and version selectors in source and target schema references.
// Validation must accept exactly one non-empty selector and reject null values, incorrect types, and simultaneous selectors.
// Revisions remain opaque identifiers, while versions reject leading zeroes, prerelease identifiers, and build metadata.
func TestDescriptorJSONSchemaSelectors(t *testing.T) {
	schema := compileDescriptorJSONSchema(t, loadDescriptorJSONSchema(t), "")
	for _, object := range []string{"sourceSchema", "targetSchema"} {
		for _, selector := range []string{"revision", "version"} {
			for _, test := range []struct {
				value         any
				validRevision bool
				validVersion  bool
			}{
				{value: "1.0.0", validRevision: true, validVersion: true},
				{value: ""},
				{value: nil},
				{value: true},
				{value: "01.0.0", validRevision: true},
				{value: "1.0.0-beta", validRevision: true},
				{value: "1.0.0+build"},
			} {
				t.Run(fmt.Sprintf("%s/%s/%v", object, selector, test.value), func(t *testing.T) {
					document := descriptorFixture(t)
					reference := descriptorObjects(document)[object]
					delete(reference, "revision")
					reference[selector] = test.value
					err := schema.Validate(document)
					valid := test.validRevision
					if selector == "version" {
						valid = test.validVersion
					}
					if valid {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
				})
			}
		}
		t.Run(object+"/both selectors", func(t *testing.T) {
			document := descriptorFixture(t)
			descriptorObjects(document)[object]["version"] = "1.0.0"
			require.Error(t, schema.Validate(document))
		})
	}
}

// TestDescriptorJSONSchemaLexicalForms checks reusable definitions for absolute URIs, versions, artifact revisions, logical types, and digests.
// Each definition must accept valid examples and reject malformed values, including trailing line separators.
// URI validation combines lexical constraints with enabled format assertions.
func TestDescriptorJSONSchemaLexicalForms(t *testing.T) {
	compiler := loadDescriptorJSONSchema(t)
	tests := []struct {
		definition string
		valid      []string
		invalid    []string
	}{
		{
			definition: "absoluteUri",
			valid:      []string{"https://example.test/schema-1.0.0.json", "urn:example:schema:1.0.0", "https://example.test/a%20b"},
			invalid:    []string{"", "schema.json", "//example.test/schema", "https://example.test/a b", "https://example.test/%zz", "https://[invalid", "https://example.test/未知", "https://example.test/\n"},
		},
		{
			definition: "schemaVersion",
			valid:      []string{"0.0.0", "1.2.3", "10.20.300"},
			invalid:    []string{"", "1", "1.0", "01.0.0", "1.01.0", "1.0.01", "-1.0.0", "1.0.0-beta", "1.0.0+build", " 1.0.0"},
		},
		{
			definition: "artifactRevision",
			valid:      []string{"a", "0", "A-z_0.9~", "...", ".r1", "01K4Q80PQ2J6R0A7KX9N3FHTVE"},
			invalid:    []string{"", ".", "..", "r/1", "r\\1", "r%31", "r:1", "r 1", "未知"},
		},
		{
			definition: "logicalType",
			valid:      []string{"a", "0", "example-cmdb-device", "a1-2b"},
			invalid:    []string{"", "A", "a_b", "a.b", "-a", "a-", "a--b", "a b", "未知"},
		},
		{
			definition: "digest",
			valid:      []string{"sha-256:" + strings.Repeat("a", 64), "sha-256:" + strings.Repeat("0123456789abcdef", 4)},
			invalid:    []string{"", "sha256:" + strings.Repeat("a", 64), "sha-256:" + strings.Repeat("A", 64), "sha-256:" + strings.Repeat("g", 64), "sha-256:" + strings.Repeat("a", 63), "sha-256:" + strings.Repeat("a", 65)},
		},
	}
	for _, test := range tests {
		t.Run(test.definition, func(t *testing.T) {
			schema := compileDescriptorJSONSchema(t, compiler, test.definition)
			for _, value := range test.valid {
				t.Run("valid/"+value, func(t *testing.T) {
					require.NoError(t, schema.Validate(value))
					for _, suffix := range []string{"\r", "\n", "\u2028", "\u2029"} {
						require.Error(t, schema.Validate(value+suffix), "trailing separator %q", suffix)
					}
				})
			}
			for _, value := range test.invalid {
				t.Run("invalid/"+value, func(t *testing.T) {
					require.Error(t, schema.Validate(value))
				})
			}
		})
	}
}
