package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// populatedDocument exercises optional metadata and every nested manifest collection through the public parser.
func populatedDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	require.NoError(t, json.Unmarshal(fixture(t, "valid-initial.json"), &document))
	schema := document["artifacts"].([]any)[0].(map[string]any)
	schema["resources"] = []any{map[string]any{
		"name": "note.txt", "role": "metadata", "mediaType": "text/plain",
		"digest": digest([]byte("note")), "size": 4, "bundlePath": "resources/note.txt",
	}}
	schema["dependencies"] = []any{map[string]any{"kind": "context", "artifactType": "device-context", "artifactRevision": "ctx-1"}}
	schema["extensions"] = map[string]any{"org.example.note": "schema metadata"}
	document["artifacts"] = []any{map[string]any{
		"kind": "context", "artifactType": "device-context", "artifactRevision": "ctx-1", "createdUnderSchemaVersion": "1.0.0",
		"canonicalUri": testOrigin + "/v1/resources/context/device-context/ctx-1",
		"payload":      map[string]any{"mediaType": artifact.ContextMediaType, "digest": digest([]byte(`{"@context":{}}`)), "size": 15, "bundlePath": "contexts/device.json"},
		"resources":    []any{}, "dependencies": []any{},
	}, schema}
	document["schemaContextBindings"] = []any{map[string]any{"schemaRevision": "rev-1", "contextRevisions": []any{"ctx-1"}}}
	document["uriCatalog"] = []any{
		map[string]any{"uri": "https://example.test/note", "artifactRevision": "rev-1", "resource": "note.txt"},
		map[string]any{"uri": testOrigin + "/v1/resources/context/device-context/ctx-1", "artifactRevision": "ctx-1"},
		map[string]any{"uri": schema["canonicalUri"], "artifactRevision": "rev-1"},
	}
	document["previousRelease"] = map[string]any{"schemaVersion": "0.9.0", "manifestDigest": digest([]byte("previous"))}
	document["extensions"] = map[string]any{"org.example.metadata": map[string]any{"enabled": true}}
	document["changes"] = map[string]any{
		"summary": "Publisher notes", "compatibility": "additive",
		"artifacts":             []any{map[string]any{"operation": "add", "kind": "schema", "artifactType": "device", "afterRevision": "rev-1", "impact": "additive"}},
		"schemaContextBindings": []any{map[string]any{"operation": "add", "schemaRevision": "rev-1", "contextRevision": "ctx-1", "impact": "additive"}},
		"uriCatalog":            []any{map[string]any{"operation": "add", "uri": "https://example.test/note", "afterRevision": "rev-1", "afterResource": "note.txt", "impact": "additive"}},
		"storageCatalog":        []any{map[string]any{"operation": "add", "kind": "schema", "artifactType": "device", "store": "data-center-inventory", "impact": "additive"}},
	}
	return document
}

// stringJSON serializes test values and stops the calling test if fixture encoding fails.
func stringJSON(t testing.TB, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

// canonicalDocument serializes a fixture with exact JSON Canonicalization Scheme bytes.
func canonicalDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	data, err := jcs.Transform([]byte(stringJSON(t, document)))
	require.NoError(t, err)
	return data
}

// documentObject selects a nested fixture object; numeric path components select array entries.
func documentObject(t *testing.T, document map[string]any, path string) map[string]any {
	t.Helper()
	var value any = document
	if path != "" {
		for _, part := range strings.Split(path, "/") {
			switch current := value.(type) {
			case map[string]any:
				value = current[part]
			case []any:
				index, err := strconv.Atoi(part)
				require.NoError(t, err)
				value = current[index]
			default:
				t.Fatalf("fixture path %q does not select an object", path)
			}
		}
	}
	object, ok := value.(map[string]any)
	require.True(t, ok, "fixture path %q must select an object", path)
	return object
}

// TestParsePopulatedManifest verifies decoded values, optional selectors, and independent authoritative byte storage.
func TestParsePopulatedManifest(t *testing.T) {
	data := canonicalDocument(t, populatedDocument(t))
	parsed, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, &PreviousRelease{SchemaVersion: "0.9.0", ManifestDigest: digest([]byte("previous"))}, parsed.PreviousRelease)
	require.Len(t, parsed.Artifacts, 2)
	schema := parsed.Artifacts[1]
	assert.Equal(t, []artifact.Resource{{Name: "note.txt", Role: "metadata", Descriptor: descriptor([]byte("note"), "text/plain", "resources/note.txt")}}, schema.Resources)
	assert.Equal(t, []artifact.Dependency{{Kind: artifact.KindContext, Type: "device-context", Revision: "ctx-1"}}, schema.Dependencies)
	assert.Equal(t, map[string]any{"org.example.note": "schema metadata"}, schema.Extensions)
	assert.Equal(t, []Binding{{SchemaRevision: "rev-1", ContextRevisions: []string{"ctx-1"}}}, parsed.SchemaContextBindings)
	assert.Equal(t, URIEntry{URI: "https://example.test/note", ArtifactRevision: "rev-1", Resource: "note.txt"}, parsed.URICatalog[0])
	assert.Equal(t, map[string]any{"org.example.metadata": map[string]any{"enabled": true}}, parsed.Extensions)
	assert.Equal(t, &Changes{
		Summary: "Publisher notes", Compatibility: CompatibilityAdditive,
		Artifacts:             []ArtifactChange{{Operation: OperationAdd, Kind: artifact.KindSchema, ArtifactType: "device", AfterRevision: "rev-1", Impact: ImpactAdditive}},
		SchemaContextBindings: []BindingChange{{Operation: OperationAdd, SchemaRevision: "rev-1", ContextRevision: "ctx-1", Impact: ImpactAdditive}},
		URICatalog:            []URIChange{{Operation: OperationAdd, URI: "https://example.test/note", AfterRevision: "rev-1", AfterResource: "note.txt", Impact: ImpactAdditive}},
		StorageCatalog:        []StorageChange{{Operation: OperationAdd, Kind: artifact.KindSchema, ArtifactType: "device", Store: StoreDataCenterInventory, Impact: ImpactAdditive}},
	}, parsed.Changes)
	assert.Equal(t, data, parsed.Raw)
	data[0] = ' '
	assert.Equal(t, byte('{'), parsed.Raw[0])
}

// TestParseRejectsNestedFieldShapes checks unknown fields and required strings at every object boundary.
func TestParseRejectsNestedFieldShapes(t *testing.T) {
	for _, node := range []struct {
		path   string
		fields []string
	}{
		{"", []string{"$schema", "formatVersion", "schemaVersion"}},
		{"previousRelease", []string{"schemaVersion", "manifestDigest"}},
		{"source", []string{"repository", "commit"}},
		{"artifacts/1", []string{"kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "canonicalUri"}},
		{"artifacts/1/payload", []string{"mediaType", "digest", "bundlePath"}},
		{"artifacts/1/resources/0", []string{"name", "role", "mediaType", "digest", "bundlePath"}},
		{"artifacts/1/dependencies/0", []string{"kind", "artifactType", "artifactRevision"}},
		{"schemaContextBindings/0", []string{"schemaRevision"}},
		{"uriCatalog/0", []string{"uri", "artifactRevision"}},
		{"storageCatalog/0", []string{"kind", "artifactType"}},
		{"changes", []string{"compatibility"}},
		{"changes/artifacts/0", []string{"operation", "kind", "artifactType", "impact"}},
		{"changes/schemaContextBindings/0", []string{"operation", "schemaRevision", "contextRevision", "impact"}},
		{"changes/uriCatalog/0", []string{"operation", "uri", "impact"}},
		{"changes/storageCatalog/0", []string{"operation", "kind", "artifactType", "store", "impact"}},
	} {
		t.Run(node.path+"/unknown", func(t *testing.T) {
			document := populatedDocument(t)
			documentObject(t, document, node.path)["unknown"] = true
			_, err := Parse(canonicalDocument(t, document))
			require.ErrorContains(t, err, `"unknown"`)
		})
		for _, field := range node.fields {
			for _, invalid := range []struct {
				name  string
				value any
			}{
				{"missing", nil}, {"null", nil}, {"empty", ""}, {"wrong type", true},
			} {
				t.Run(node.path+"/"+field+"/"+invalid.name, func(t *testing.T) {
					document := populatedDocument(t)
					object := documentObject(t, document, node.path)
					if invalid.name == "missing" {
						delete(object, field)
					} else {
						object[field] = invalid.value
					}
					_, err := Parse(canonicalDocument(t, document))
					require.ErrorContains(t, err, field+" must be non-empty string")
				})
			}
		}
	}
}

// TestParseRejectsCollectionAndObjectShapes checks missing collections, non-object entries, and optional-field types.
func TestParseRejectsCollectionAndObjectShapes(t *testing.T) {
	for _, test := range []struct {
		path, field string
		value       any
		want        string
	}{
		{"", "previousRelease", true, "previousRelease must be null or an object"},
		{"", "source", nil, "source must be an object"},
		{"", "artifacts", nil, "artifacts must be array"},
		{"", "artifacts", []any{nil}, "artifact must be object"},
		{"artifacts/1", "payload", nil, "descriptor must be object"},
		{"artifacts/1", "resources", nil, "resources must be array"},
		{"artifacts/1", "resources", []any{nil}, "resource must be object"},
		{"artifacts/1", "dependencies", nil, "dependencies must be array"},
		{"artifacts/1", "dependencies", []any{nil}, "dependency must be object"},
		{"", "schemaContextBindings", nil, "schemaContextBindings must be array"},
		{"", "schemaContextBindings", []any{nil}, "binding must be object"},
		{"schemaContextBindings/0", "contextRevisions", nil, "contextRevisions must be array"},
		{"schemaContextBindings/0", "contextRevisions", []any{}, "context revisions must not be empty"},
		{"schemaContextBindings/0", "contextRevisions", []any{true}, "must contain non-empty strings"},
		{"", "uriCatalog", nil, "uriCatalog must be array"},
		{"", "uriCatalog", []any{nil}, "URI entry must be object"},
		{"uriCatalog/0", "resource", nil, "resource must be non-empty string"},
		{"artifacts/1", "schemaClass", nil, "schemaClass must be non-empty string"},
		{"artifacts/1", "targetSchemaVersion", nil, "targetSchemaVersion must be non-empty string"},
		{"", "storageCatalog", nil, "storageCatalog must be array"},
		{"", "storageCatalog", []any{nil}, "storage entry must be object"},
		{"storageCatalog/0", "stores", nil, "stores must be array"},
		{"", "extensions", nil, "extensions must be object"},
		{"artifacts/1", "extensions", nil, "extensions must be object"},
		{"", "extensions", map[string]any{"unqualified": true}, "invalid extension name"},
		{"", "changes", nil, "changes must be an object"},
		{"changes", "summary", nil, "summary must be non-empty string"},
		{"changes", "artifacts", nil, "artifact changes must be array"},
		{"changes", "artifacts", []any{nil}, "artifact change must be object"},
		{"changes", "schemaContextBindings", nil, "binding changes must be array"},
		{"changes", "schemaContextBindings", []any{nil}, "binding change must be object"},
		{"changes", "uriCatalog", nil, "URI changes must be array"},
		{"changes", "uriCatalog", []any{nil}, "URI change must be object"},
		{"changes", "storageCatalog", nil, "storage changes must be array"},
		{"changes", "storageCatalog", []any{nil}, "storage change must be object"},
	} {
		t.Run(test.path+"/"+test.field+"/"+test.want, func(t *testing.T) {
			document := populatedDocument(t)
			documentObject(t, document, test.path)[test.field] = test.value
			_, err := Parse(canonicalDocument(t, document))
			require.ErrorContains(t, err, test.want)
		})
	}
}

// TestParseRejectsInvalidMetadata isolates lexical, descriptor, and operation-specific validation failures.
func TestParseRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		path, field string
		value       any
		want        string
	}{
		{"previousRelease", "schemaVersion", "1.0", "invalid previous release"},
		{"previousRelease", "manifestDigest", "invalid", "invalid previous release"},
		{"artifacts/1", "schemaClass", "unknown", "invalid schema fields"},
		{"artifacts/0", "schemaClass", "entity-type", "invalid context fields"},
		{"artifacts/1/payload", "size", -1, "invalid size"},
		{"artifacts/1/payload", "size", 1.5, "invalid size"},
		{"artifacts/1/payload", "size", "4", "size must be number"},
		{"artifacts/1/payload", "digest", "invalid", "invalid payload descriptor"},
		{"changes/artifacts/0", "beforeRevision", "rev-0", "invalid artifact change"},
		{"changes/artifacts/0", "afterRevision", nil, "afterRevision must be non-empty string"},
		{"changes/uriCatalog/0", "beforeResource", nil, "beforeResource must be non-empty string"},
		{"changes/uriCatalog/0", "afterResource", "../note", "invalid URI change"},
	} {
		t.Run(test.path+"/"+test.field+"/"+test.want, func(t *testing.T) {
			document := populatedDocument(t)
			documentObject(t, document, test.path)[test.field] = test.value
			_, err := Parse(canonicalDocument(t, document))
			require.ErrorContains(t, err, test.want)
		})
	}
	t.Run("missing size", func(t *testing.T) {
		document := populatedDocument(t)
		delete(documentObject(t, document, "artifacts/1/payload"), "size")
		_, err := Parse(canonicalDocument(t, document))
		require.ErrorContains(t, err, "size is required")
	})
	t.Run("non-breaking artifact removal", func(t *testing.T) {
		document := populatedDocument(t)
		change := documentObject(t, document, "changes/artifacts/0")
		change["operation"], change["beforeRevision"] = "remove", "rev-1"
		delete(change, "afterRevision")
		_, err := Parse(canonicalDocument(t, document))
		require.ErrorContains(t, err, "artifact removal must be breaking")
	})
}

// TestParseChangeEnums checks registered values, unknown values, JSON types, and field-specific operation restrictions.
func TestParseChangeEnums(t *testing.T) {
	for _, test := range []struct {
		name   string
		field  string
		values []any
	}{
		{"compatibility", "compatibility", []any{"initial", "breaking", "additive", "operational"}},
		{"artifact operation", "operation", []any{"add", "update", "remove"}},
		{"artifact impact", "impact", []any{"breaking", "additive", "operational"}},
		{"binding operation", "operation", []any{"add", "remove"}},
		{"binding impact", "impact", []any{"breaking", "additive", "operational"}},
		{"URI operation", "operation", []any{"add", "update", "remove"}},
		{"URI impact", "impact", []any{"breaking", "additive", "operational"}},
		{"storage operation", "operation", []any{"add", "remove"}},
		{"storage impact", "impact", []any{"breaking", "additive", "operational"}},
		{"store", "store", []any{"data-center-inventory", "telemetry-store", "insights-repository", "recorded-actions-repository"}},
	} {
		values := append(append([]any(nil), test.values...), "unknown", "", nil, 1)
		if test.field == "impact" {
			values = append(values, "initial")
		}
		if test.name == "binding operation" || test.name == "storage operation" {
			values = append(values, "update")
		}
		for index, value := range values {
			t.Run(test.name+"/"+stringJSON(t, value), func(t *testing.T) {
				var object map[string]any
				require.NoError(t, json.Unmarshal(fixture(t, "valid-initial.json"), &object))
				changes := map[string]any{
					"compatibility": "initial", "artifacts": []any{}, "schemaContextBindings": []any{},
					"uriCatalog": []any{}, "storageCatalog": []any{},
				}
				entry := map[string]any{"operation": "add", "impact": "breaking"}
				switch test.name {
				case "compatibility":
					entry = changes
				case "artifact operation", "artifact impact":
					entry["kind"], entry["artifactType"] = "schema", "device"
					entry["afterRevision"] = "rev-2"
					if value == "update" || value == "remove" {
						entry["beforeRevision"] = "rev-1"
					}
					if value == "remove" {
						delete(entry, "afterRevision")
					}
					changes["artifacts"] = []any{entry}
				case "binding operation", "binding impact":
					entry["schemaRevision"], entry["contextRevision"] = "rev-1", "ctx-1"
					changes["schemaContextBindings"] = []any{entry}
				case "URI operation", "URI impact":
					entry["uri"], entry["afterRevision"] = "https://example.test/device", "rev-2"
					if value == "update" || value == "remove" {
						entry["beforeRevision"] = "rev-1"
					}
					if value == "remove" {
						delete(entry, "afterRevision")
					}
					changes["uriCatalog"] = []any{entry}
				case "storage operation", "storage impact", "store":
					entry["kind"], entry["artifactType"], entry["store"] = "schema", "device", "telemetry-store"
					changes["storageCatalog"] = []any{entry}
				}
				entry[test.field] = value
				object["changes"] = changes
				parsed, err := Parse(canonicalDocument(t, object))
				if index < len(test.values) {
					require.NoError(t, err)
					require.NotNil(t, parsed.Changes)
				} else {
					require.Error(t, err)
				}
			})
		}
	}
}

// TestParseFromFile verifies file parsing preserves the results and errors returned by Parse.
func TestParseFromFile(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"valid manifest", canonicalDocument(t, populatedDocument(t))},
		{"invalid structure", fixture(t, "invalid-structural-unknown-field.json")},
		{"trailing newline", fixtureRaw(t, "invalid-canonical-trailing-newline.json")},
		{"noncanonical JSON", append([]byte(" "), fixture(t, "valid-initial.json")...)},
		{"malformed JSON", []byte(`{"schemaVersion":`)},
		{"empty file", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			require.NoError(t, os.WriteFile(path, test.data, 0600))
			want, wantErr := Parse(test.data)

			got, err := ParseFromFile(path)
			if wantErr != nil {
				require.EqualError(t, err, wantErr.Error())
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, want, got)
		})
	}
}

// TestParseFromFileReadErrors verifies filesystem errors remain inspectable and return no partial manifest.
func TestParseFromFileReadErrors(t *testing.T) {
	directory := t.TempDir()
	for _, test := range []struct {
		name string
		path string
	}{
		{"missing file", filepath.Join(directory, "missing.json")},
		{"directory", directory},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseFromFile(test.path)
			require.ErrorContains(t, err, "read manifest")
			var pathErr *os.PathError
			require.ErrorAs(t, err, &pathErr)
			assert.Equal(t, test.path, pathErr.Path)
			if test.name == "missing file" {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
			assert.Equal(t, Manifest{}, parsed)
		})
	}
}
