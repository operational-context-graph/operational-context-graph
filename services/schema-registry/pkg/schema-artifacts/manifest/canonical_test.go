package manifest

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/require"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// TestPublishedJCSVectors pins independent RFC 8785 sections 3.2.2 and 3.2.3 examples.
func TestPublishedJCSVectors(t *testing.T) {
	for _, vector := range []struct{ input, want string }{
		{`{"numbers":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001],"string":"\u20ac$\u000F\nA'B\"\\\"/","literals":[null,true,false]}`,
			`{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\"/"}`},
		{`{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`,
			"{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"ö\":\"Latin Small Letter O With Diaeresis\",\"€\":\"Euro Sign\",\"😀\":\"Emoji: Grinning Face\",\"דּ\":\"Hebrew Letter Dalet With Dagesh\"}"},
		{`{"b":-0,"a":[3,2,1]}`, `{"a":[3,2,1],"b":0}`},
	} {
		got, err := jcs.Transform([]byte(vector.input))
		require.NoError(t, err)
		require.Equal(t, vector.want, string(got))
	}
}

// TestCanonicalizeEveryArray verifies all normative keys, strict duplicate rejection, and non-mutating reader validation.
func TestCanonicalizeEveryArray(t *testing.T) {
	for _, test := range []struct{ path, field, key, low, high string }{
		{"", "artifacts", "artifactType", "alpha", "device"},
		{"artifacts/1", "resources", "name", "a.txt", "note.txt"},
		{"artifacts/1", "dependencies", "artifactType", "alpha", "device-context"},
		{"", "schemaContextBindings", "schemaRevision", "rev-0", "rev-1"},
		{"schemaContextBindings/0", "contextRevisions", "", "ctx-0", "ctx-1"},
		{"", "uriCatalog", "uri", "https://a.test/", "https://example.test/note"},
		{"", "storageCatalog", "artifactType", "alpha", "device"},
		{"storageCatalog/0", "stores", "", "data-center-inventory", "telemetry-store"},
		{"changes", "artifacts", "artifactType", "alpha", "device"},
		{"changes", "schemaContextBindings", "contextRevision", "ctx-0", "ctx-1"},
		{"changes", "uriCatalog", "uri", "https://a.test/", "https://example.test/note"},
		{"changes", "storageCatalog", "store", "data-center-inventory", "telemetry-store"},
	} {
		t.Run(test.path+"/"+test.field, func(t *testing.T) {
			document := populatedDocument(t)
			object := documentObject(t, document, test.path)
			var low, high any = test.low, test.high
			if test.key != "" {
				original := object[test.field].([]any)[0].(map[string]any)
				a, b := map[string]any{}, map[string]any{}
				for key, value := range original {
					a[key], b[key] = value, value
				}
				a[test.key], b[test.key] = test.low, test.high
				if test.field == "resources" {
					a["bundlePath"] = "resources/a.txt"
				}
				if test.field == "artifacts" && test.path == "" {
					payload := map[string]any{}
					maps.Copy(payload, a["payload"].(map[string]any))
					payload["bundlePath"] = "contexts/alpha.json"
					a["payload"] = payload
				}
				low, high = a, b
			}
			object[test.field] = []any{high, low}
			unordered := canonicalDocument(t, document)
			_, err := Parse(unordered)
			require.ErrorContains(t, err, "not ordered")
			got, err := Canonicalize(unordered)
			require.NoError(t, err)
			object[test.field] = []any{low, high}
			require.Equal(t, canonicalDocument(t, document), got)
			again, err := Canonicalize(got)
			require.NoError(t, err)
			require.Equal(t, got, again)
			object[test.field] = []any{low, low}
			_, err = Canonicalize(canonicalDocument(t, document))
			require.Error(t, err, "duplicates MUST NOT be silently deduplicated")
		})
	}
}

// TestCanonicalizeUnicode preserves normalization and publisher array order while sorting catalog keys by UTF-16.
func TestCanonicalizeUnicode(t *testing.T) {
	var document map[string]any
	require.NoError(t, json.Unmarshal(fixture(t, "valid-initial.json"), &document))
	document["extensions"] = map[string]any{"org.example.vector": map[string]any{
		"\ue000": "private", "\U00010000": "supplementary", "é": "composed", "e\u0301": "decomposed", "array": []any{"z", "a"},
	}}
	document["uriCatalog"] = []any{
		map[string]any{"uri": "https://example.test/\ue000", "artifactRevision": "rev-1"},
		map[string]any{"uri": "https://example.test/\U00010000", "artifactRevision": "rev-1"},
	}
	got, err := Canonicalize([]byte(stringJSON(t, document)))
	require.NoError(t, err)
	require.Less(t, bytes.Index(got, []byte("https://example.test/\U00010000")), bytes.Index(got, []byte("https://example.test/\ue000")))
	require.Contains(t, string(got), `"array":["z","a"]`)
	require.Contains(t, string(got), `"é":"composed"`)
	require.Contains(t, string(got), "\"e\u0301\":\"decomposed\"")
}

// TestCanonicalResourceSortingPreservesTransformations keeps manifest sets separate from immutable descriptor execution order.
func TestCanonicalResourceSortingPreservesTransformations(t *testing.T) {
	document := populatedDocument(t)
	item := documentObject(t, document, "artifacts/1")
	item["kind"], item["artifactType"], item["artifactRevision"] = "source-mapping", "mapping-a", "mapping-a-r1"
	delete(item, "schemaClass")
	item["targetSchemaVersion"] = "1.0.0"
	item["canonicalUri"] = testOrigin + "/v1/resources/source-mapping/mapping-a/mapping-a-r1"
	var payload map[string]any
	require.NoError(t, json.Unmarshal(mappingPayload(t, "mapping-a"), &payload))
	programs := payload["programs"].(map[string]any)
	transformations := programs["transformations"].([]any)
	resources := make([]any, 0, 3)
	for i, name := range []string{"z.bin", "a.bin", "shared.bin"} {
		resources = append(resources, map[string]any{
			"name": name, "role": "program", "mediaType": "application/octet-stream",
			"digest": digest([]byte{0, 255}), "size": 2, "bundlePath": "mapping/" + name,
		})
		if i < len(transformations) {
			transformations[i].(map[string]any)["programRef"] = name
		}
	}
	item["resources"] = resources
	descriptorBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	before := slices.Clone(descriptorBytes)
	item["payload"] = map[string]any{"mediaType": sourcemapping.DescriptorMediaType, "digest": digest(before), "size": len(before), "bundlePath": "mapping/descriptor.json"}
	canonical, err := Canonicalize([]byte(stringJSON(t, document)))
	require.NoError(t, err)
	parsed, err := Parse(canonical)
	require.NoError(t, err)
	resourcesSorted := parsed.Artifacts[1].Resources
	require.Equal(t, []string{"a.bin", "shared.bin", "z.bin"}, []string{resourcesSorted[0].Name, resourcesSorted[1].Name, resourcesSorted[2].Name})
	require.Equal(t, before, descriptorBytes)
	require.Equal(t, digest(before), parsed.Artifacts[1].Payload.Digest)
	descriptor, err := sourcemapping.Parse(descriptorBytes)
	require.NoError(t, err)
	require.Equal(t, "z.bin", descriptor.Programs.Transformations[0].ProgramRef)
	require.Equal(t, "a.bin", descriptor.Programs.Transformations[1].ProgramRef)
	require.Equal(t, "entity", descriptor.Programs.Transformations[0].TargetSchema.ID)
	require.Equal(t, "edge", descriptor.Programs.Transformations[1].TargetSchema.ID)
}

// TestCanonicalizeExactByteBoundary exercises the real manifest limit with individually bounded extension strings.
func TestCanonicalizeExactByteBoundary(t *testing.T) {
	var document map[string]any
	require.NoError(t, json.Unmarshal(fixture(t, "valid-initial.json"), &document))
	padding := make([]string, 17)
	document["extensions"] = map[string]any{"org.example.padding": padding}
	base := canonicalDocument(t, document)
	remaining := schemaartifacts.MaxArtifactBytes - len(base)
	for i := range padding {
		size := min(remaining, schemaartifacts.MaxJSONStringBytes)
		padding[i] = strings.Repeat("a", size)
		remaining -= size
	}
	require.Zero(t, remaining)
	data := canonicalDocument(t, document)
	require.Len(t, data, schemaartifacts.MaxArtifactBytes)
	got, err := Canonicalize(data)
	require.NoError(t, err)
	require.Equal(t, data, got)
	_, err = Parse(data)
	require.NoError(t, err)
	_, err = Canonicalize(append(data, ' '))
	require.ErrorIs(t, err, strictjson.ErrTooLarge)
	_, err = Parse(append(data, ' '))
	require.ErrorContains(t, err, "manifest exceeds byte limit")
}

// TestCanonicalizeInvalidInputs verifies strict parsing before sorting and exact submitted-byte requirements afterward.
func TestCanonicalizeInvalidInputs(t *testing.T) {
	for _, data := range [][]byte{nil, {}, []byte(`null`), []byte(`{"artifacts":[null]}`), []byte(`{"a":1,"a":2}`), append([]byte{0xef, 0xbb, 0xbf}, fixture(t, "valid-initial.json")...)} {
		got, err := Canonicalize(data)
		require.Error(t, err)
		require.Nil(t, got)
	}
	data := fixture(t, "valid-initial.json")
	got, err := Canonicalize(append(slices.Clone(data), '\n'))
	require.NoError(t, err)
	require.Equal(t, data, got)
	_, err = Parse(append(slices.Clone(data), '\n'))
	require.ErrorContains(t, err, "trailing newline")
	var document map[string]any
	require.NoError(t, json.Unmarshal(data, &document))
	document["manifestDigest"] = digest(data)
	_, err = Canonicalize(canonicalDocument(t, document))
	require.ErrorContains(t, err, "manifest fields")
	delete(document, "manifestDigest")
	document["extensions"] = map[string]any{"org.example.text": "sentinel"}
	for _, surrogate := range []string{`\ud800`, `\udfff`} {
		input := bytes.ReplaceAll(canonicalDocument(t, document), []byte("sentinel"), []byte(surrogate))
		_, err := Canonicalize(input)
		require.ErrorIs(t, err, ErrInvalid, "lone surrogates MUST NOT become replacement characters")
	}
}

// TestChangeOrderingDoesNotMutateSharedSlices rejects descending and duplicate keys without modifying caller state under concurrency.
func TestChangeOrderingDoesNotMutateSharedSlices(t *testing.T) {
	m, err := Parse(canonicalDocument(t, populatedDocument(t)))
	require.NoError(t, err)
	m.Changes.URICatalog = []URIChange{
		{URI: "https://z.test/", Operation: OperationAdd}, {URI: "https://a.test/", Operation: OperationAdd},
	}
	before := slices.Clone(m.Changes.URICatalog)
	var group sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		group.Go(func() { errors <- m.validateOrder() })
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.ErrorContains(t, err, "change arrays are not ordered")
	}
	require.Equal(t, before, m.Changes.URICatalog)
}
