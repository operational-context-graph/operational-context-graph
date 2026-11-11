package manifest

import (
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"
)

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
				data, err := jcs.Transform([]byte(stringJSON(t, object)))
				require.NoError(t, err)
				parsed, err := Parse(data)
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

// stringJSON serializes test values and stops the calling test if fixture encoding fails.
func stringJSON(t testing.TB, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
