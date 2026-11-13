package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
)

// TestChangesValidatesParametersAndSupportsConditionalResponse checks query validation, response links,
// and conditional GET and HEAD behavior.
func TestChangesValidatesParametersAndSupportsConditionalResponse(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	artifacts := map[string]model.Artifact{"rev-device-v1": {Revision: "rev-device-v1", Payload: model.ByteDescriptor{MediaType: "application/schema+json", Bytes: []byte("true")}}}
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, artifacts: artifacts, releases: []model.Release{
		{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2},
		{SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1},
	}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"fromSchemaVersion":"1.0.0"`)
	assert.Contains(t, response.Body.String(), `"toSchemaVersion":"1.0.1"`)
	assert.Contains(t, response.Body.String(), `"compatibility":`)
	assert.Contains(t, response.Body.String(), `"artifacts":`)
	assert.Contains(t, response.Body.String(), `"schemaContextBindings":`)
	assert.Contains(t, response.Body.String(), `"uriCatalog":`)
	assert.Contains(t, response.Body.String(), `"storageCatalog":`)
	assert.Contains(t, response.Body.String(), `"from":"/v1/releases/1.0.0"`)
	assert.Contains(t, response.Body.String(), `"to":"/v1/releases/1.0.1"`)
	var body v1.Changes
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "1.0.0", body.FromSchemaVersion)
	assert.Equal(t, "1.0.1", body.ToSchemaVersion)
	tag := response.Header().Get("ETag")
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil)
	request.Header.Set("If-None-Match", tag)
	server.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotModified, response.Code)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, tag, response.Header().Get("ETag"))
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Body.String())
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0&from=1.0.1&to=1.0.1"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=&to=1.0.1"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=not-a-version&to=1.0.1"))
}

// TestChangesMapsReleaseAndStorageFailures checks missing releases, invalid lineage, and storage
// failures through the comparison route.
func TestChangesMapsReleaseAndStorageFailures(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, releases: []model.Release{
		{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2},
		{SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1},
	}}
	server := releaseTestServer(snapshot)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/releases/9.0.0", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/v1/changes?from=1.0.0&to=9.0.0", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "1.0.1", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.0&to=1.0.0"))
	assert.Equal(t, http.StatusBadRequest, serveStatus(server, "/v1/changes?from=1.0.1&to=1.0.0"))
	disconnected := false
	snapshot.ancestor = &disconnected
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"versions-not-comparable"`)
	snapshot.ancestor = nil
	snapshot.lineageErr = postgres.ErrInconsistentState
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	snapshot.lineageErr = nil

	server.releases = release.New(releaseTestReader{err: errors.New("database unavailable")})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"registry-unavailable"`)
}

// TestChangesReadsControlledSnapshotConcurrently checks deterministic response bodies and ETags
// across concurrent comparisons of a fixed snapshot.
func TestChangesReadsControlledSnapshotConcurrently(t *testing.T) {
	first := mustTestManifest(t)
	second := versionedManifest(t, first, "1.0.1", digest(first))
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1", ActivationSequence: 2, SnapshotSequence: 2}, artifacts: map[string]model.Artifact{"fixture": {Revision: "fixture", Payload: model.ByteDescriptor{MediaType: "application/schema+json", Bytes: []byte("true")}}}, releases: []model.Release{{SchemaVersion: "1.0.1", ManifestDigest: digest(second), ManifestBytes: second, ActivationSequence: 2}, {SchemaVersion: "1.0.0", ManifestDigest: digest(first), ManifestBytes: first, ActivationSequence: 1}}}
	server := releaseTestServer(snapshot)
	var group sync.WaitGroup
	results := make(chan struct {
		body, tag string
		status    int
	}, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/changes?from=1.0.0&to=1.0.1", nil))
			results <- struct {
				body, tag string
				status    int
			}{response.Body.String(), response.Header().Get("ETag"), response.Code}
		}()
	}
	group.Wait()
	close(results)
	var firstResult struct {
		body, tag string
		status    int
	}
	for result := range results {
		if firstResult.tag == "" {
			firstResult = result
		}
		assert.Equal(t, http.StatusOK, result.status)
		assert.Equal(t, firstResult.body, result.body)
		assert.Equal(t, firstResult.tag, result.tag)
	}
}

// TestMappingChangesUseConservativeClassifierThroughHTTP distinguishes mappings added for existing
// sources from mappings added for new sources without executing their programs.
func TestMappingChangesUseConservativeClassifierThroughHTTP(t *testing.T) {
	for _, test := range []struct{ name, source, version, impact string }{{"existing source", "device", "2.0.0", "breaking"}, {"new source", "new-source", "1.1.0", "additive"}} {
		t.Run(test.name, func(t *testing.T) {
			before := mustTestManifest(t)
			var document map[string]any
			require.NoError(t, json.Unmarshal(before, &document))
			entries := document["artifacts"].([]any)
			schema := entries[0].(map[string]any)
			sourceRevision := schema["artifactRevision"].(string)
			if test.source != "device" {
				copy := make(map[string]any)
				for key, value := range schema {
					copy[key] = value
				}
				sourceRevision = "new-source-r1"
				copy["artifactType"], copy["artifactRevision"], copy["schemaClass"], copy["createdUnderSchemaVersion"] = test.source, sourceRevision, "source-record", test.version
				copy["canonicalUri"] = "https://registry.example.test/v1/resources/schema/" + test.source + "/" + sourceRevision
				payloadCopy := make(map[string]any)
				for key, value := range schema["payload"].(map[string]any) {
					payloadCopy[key] = value
				}
				payloadCopy["bundlePath"] = "schemas/" + test.source + ".json"
				copy["payload"] = payloadCopy
				entries = append(entries, copy)
				document["uriCatalog"] = append(document["uriCatalog"].([]any), map[string]any{"uri": copy["canonicalUri"], "artifactRevision": sourceRevision})
			}
			programs := map[string]any{"condition": map[string]any{"programRef": "opaque.bin", "programDigest": fixtureBytes([]byte("opaque"), "application/octet-stream").Digest}, "transformations": []any{map[string]any{"programRef": "opaque.bin", "programDigest": fixtureBytes([]byte("opaque"), "application/octet-stream").Digest, "cardinality": "many", "targetSchema": map[string]any{"id": "device", "revision": schema["artifactRevision"]}}}}
			transformations := programs["transformations"].([]any)
			programs["transformations"] = append(transformations, transformations[0])
			payload, err := json.Marshal(map[string]any{"$schema": "https://example.test/mapping-1.0.0", "formatVersion": "1.0.0", "id": "device-mapping", "sourceSchema": map[string]any{"id": test.source, "revision": sourceRevision}, "engine": map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque"}, "programs": programs})
			require.NoError(t, err)
			payloadDescriptor := fixtureBytes(payload, "application/vnd.apeiro.ocg.source-mapping.v1+json")
			entry := map[string]any{"kind": "source-mapping", "artifactType": "device-mapping", "artifactRevision": "mapping-added", "createdUnderSchemaVersion": test.version, "targetSchemaVersion": test.version, "canonicalUri": "https://registry.example.test/v1/resources/source-mapping/device-mapping/mapping-added", "payload": map[string]any{"mediaType": payloadDescriptor.MediaType, "digest": payloadDescriptor.Digest, "size": payloadDescriptor.Size, "bundlePath": "mapping/descriptor.json"}, "resources": []any{}, "dependencies": []any{}}
			programDescriptor := fixtureBytes([]byte("opaque"), "application/octet-stream")
			entry["resources"] = []any{map[string]any{"name": "opaque.bin", "role": "program", "mediaType": programDescriptor.MediaType, "digest": programDescriptor.Digest, "size": programDescriptor.Size, "bundlePath": "mapping/opaque.bin"}}
			dependencies := []any{map[string]any{"kind": "schema", "artifactType": "device", "artifactRevision": schema["artifactRevision"]}}
			if test.source != "device" {
				dependencies = append(dependencies, map[string]any{"kind": "schema", "artifactType": test.source, "artifactRevision": sourceRevision})
			}
			entry["dependencies"] = dependencies
			document["artifacts"] = append(entries, entry)
			document["schemaVersion"] = test.version
			document["previousRelease"] = map[string]any{"schemaVersion": "1.0.0", "manifestDigest": digest(before)}
			delete(document, "changes")
			document["uriCatalog"] = append(document["uriCatalog"].([]any), map[string]any{"uri": entry["canonicalUri"], "artifactRevision": "mapping-added"})
			var secondPayloadDocument map[string]any
			require.NoError(t, json.Unmarshal(payload, &secondPayloadDocument))
			secondPayloadDocument["id"] = "device-mapping-two"
			secondPayload, err := json.Marshal(secondPayloadDocument)
			require.NoError(t, err)
			secondDescriptor := fixtureBytes(secondPayload, payloadDescriptor.MediaType)
			copyBytes, err := json.Marshal(entry)
			require.NoError(t, err)
			var secondEntry map[string]any
			require.NoError(t, json.Unmarshal(copyBytes, &secondEntry))
			secondEntry["artifactType"], secondEntry["artifactRevision"] = "device-mapping-two", "mapping-added-two"
			secondEntry["canonicalUri"] = "https://registry.example.test/v1/resources/source-mapping/device-mapping-two/mapping-added-two"
			secondEntry["payload"] = map[string]any{"mediaType": secondDescriptor.MediaType, "digest": secondDescriptor.Digest, "size": secondDescriptor.Size, "bundlePath": "mapping-two/descriptor.json"}
			secondEntry["resources"].([]any)[0].(map[string]any)["bundlePath"] = "mapping-two/opaque.bin"
			document["artifacts"] = append(document["artifacts"].([]any), secondEntry)
			uris := append(document["uriCatalog"].([]any), map[string]any{"uri": secondEntry["canonicalUri"], "artifactRevision": "mapping-added-two"})
			sort.Slice(uris, func(i, j int) bool {
				return uris[i].(map[string]any)["uri"].(string) < uris[j].(map[string]any)["uri"].(string)
			})
			document["uriCatalog"] = uris
			encoded, err := json.Marshal(document)
			require.NoError(t, err)
			after, err := jcs.Transform(encoded)
			require.NoError(t, err)
			artifacts := map[string]model.Artifact{schema["artifactRevision"].(string): {Payload: fixtureBytes([]byte("true"), "application/schema+json")}, "mapping-added": {Payload: payloadDescriptor}, "new-source-r1": {Payload: fixtureBytes([]byte("true"), "application/schema+json")}}
			artifacts["mapping-added-two"] = model.Artifact{Payload: secondDescriptor}
			snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: test.version, ActivationSequence: 2, SnapshotSequence: 2}, releases: []model.Release{{SchemaVersion: "1.0.0", ManifestBytes: before, ActivationSequence: 1}, {SchemaVersion: test.version, ManifestBytes: after, ActivationSequence: 2}}, artifacts: artifacts}
			server := releaseTestServer(snapshot)
			path := "/v1/changes?from=1.0.0&to=" + test.version
			response := serveArtifactRequest(server, http.MethodGet, path)
			body := mappingJSON(t, response)
			require.Equal(t, test.impact, body["compatibility"])
			mappingChanges := 0
			for _, value := range body["artifacts"].([]any) {
				if value.(map[string]any)["kind"] == "source-mapping" {
					mappingChanges++
				}
			}
			require.Equal(t, 2, mappingChanges)
			require.Equal(t, "1.0.0", body["fromSchemaVersion"])
			require.Equal(t, test.version, body["toSchemaVersion"])
			require.Equal(t, test.version, response.Header().Get("Schema-Version"))
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("If-None-Match", response.Header().Get("ETag"))
			conditional := httptest.NewRecorder()
			server.Handler().ServeHTTP(conditional, request)
			require.Equal(t, http.StatusNotModified, conditional.Code)
			head := serveArtifactRequest(server, http.MethodHead, path)
			require.Equal(t, http.StatusOK, head.Code)
			require.Empty(t, head.Body.String())
			var group sync.WaitGroup
			results := make(chan int, 8)
			for range 8 {
				group.Add(1)
				go func() { defer group.Done(); results <- serveArtifactRequest(server, http.MethodGet, path).Code }()
			}
			group.Wait()
			close(results)
			for status := range results {
				require.Equal(t, http.StatusOK, status)
			}
		})
	}
}

// comparisonDocument decodes fixture bytes into an editable JSON object or fails the test.
func comparisonDocument(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	require.NoError(t, json.Unmarshal(data, &value))
	return value
}

// comparisonBytes canonicalizes a fixture manifest or fails the test on structural errors.
func comparisonBytes(t *testing.T, document map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	require.NoError(t, err)
	data, err = manifest.Canonicalize(data)
	require.NoError(t, err)
	return data
}

// comparisonPayload updates a payload descriptor and returns its byte-backed storage record.
func comparisonPayload(entry map[string]any, payload []byte, media string) model.Artifact {
	bytes := fixtureBytes(payload, media)
	entry["payload"] = map[string]any{"mediaType": media, "digest": bytes.Digest, "size": bytes.Size, "bundlePath": "artifacts/" + entry["artifactType"].(string) + ".json"}
	return model.Artifact{Payload: bytes}
}

// comparisonServer builds a read-only server over fixed 1.0.0 and 2.0.0 release snapshots.
func comparisonServer(t *testing.T, before, after []byte, artifacts map[string]model.Artifact) *Server {
	t.Helper()
	snapshot := &releaseTestSnapshot{current: model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 2}, artifacts: artifacts,
		releases: []model.Release{{SchemaVersion: "1.0.0", ManifestBytes: before, ActivationSequence: 1}, {SchemaVersion: "2.0.0", ManifestBytes: after, ActivationSequence: 2}}}
	return releaseTestServer(snapshot)
}

// assertComparisonResponses checks GET, HEAD, conditional, and concurrent responses, then returns
// the decoded change summary.
func assertComparisonResponses(t *testing.T, server *Server) v1.Changes {
	t.Helper()
	const path = "/v1/changes?from=1.0.0&to=2.0.0"
	get := serveArtifactRequest(server, http.MethodGet, path)
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var value v1.Changes
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &value))
	require.Equal(t, "1.0.0", value.FromSchemaVersion)
	require.Equal(t, "2.0.0", value.ToSchemaVersion)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("If-None-Match", get.Header().Get("ETag"))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		require.Equal(t, http.StatusNotModified, response.Code)
		require.Empty(t, response.Body.String())
		require.Equal(t, "2.0.0", response.Header().Get("Schema-Version"))
	}
	head := serveArtifactRequest(server, http.MethodHead, path)
	require.Equal(t, http.StatusOK, head.Code)
	require.Equal(t, get.Header().Get("ETag"), head.Header().Get("ETag"))
	require.Empty(t, head.Body.String())
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			response := serveArtifactRequest(server, http.MethodGet, path)
			assert.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, get.Body.String(), response.Body.String())
		})
	}
	group.Wait()
	return value
}

// TestChangesSchemaClassification checks schema impacts through HTTP while preserving breaking
// overall impact from canonical URI removal.
func TestChangesSchemaClassification(t *testing.T) {
	var vectors struct {
		Cases []struct {
			ID            string
			Before, After json.RawMessage
			Impact        manifest.Impact
		}
	}
	require.NoError(t, json.Unmarshal(corpus.ReadFixture(t, "schema-changes.json"), &vectors))
	for _, vector := range vectors.Cases {
		t.Run(vector.ID, func(t *testing.T) {
			doc := comparisonDocument(t, mustTestManifest(t))
			entry := doc["artifacts"].([]any)[0].(map[string]any)
			old := comparisonPayload(entry, vector.Before, artifact.SchemaMediaType)
			before := comparisonBytes(t, doc)
			doc = comparisonDocument(t, versionedManifest(t, before, "2.0.0", digest(before)))
			entry = doc["artifacts"].([]any)[0].(map[string]any)
			entry["artifactRevision"], entry["createdUnderSchemaVersion"] = "rev-2", "2.0.0"
			entry["canonicalUri"] = "https://registry.example.test/v1/resources/schema/device/rev-2"
			fresh := comparisonPayload(entry, vector.After, artifact.SchemaMediaType)
			doc["uriCatalog"] = []any{map[string]any{"uri": entry["canonicalUri"], "artifactRevision": "rev-2"}}
			after := comparisonBytes(t, doc)
			value := assertComparisonResponses(t, comparisonServer(t, before, after, map[string]model.Artifact{"rev-1": old, "rev-2": fresh}))
			require.Len(t, value.Artifacts, 1)
			require.Equal(t, vector.Impact, value.Artifacts[0].Impact)
			// Removing the old canonical URI independently makes the complete release breaking.
			require.Equal(t, manifest.CompatibilityBreaking, value.Compatibility)
		})
	}
}

// TestChangesURIResourcesAndStorage checks same-revision retargeting and breaking additions to
// existing storage destinations.
func TestChangesURIResourcesAndStorage(t *testing.T) {
	for _, resourceUpdate := range []bool{false, true} {
		doc := comparisonDocument(t, mustTestManifest(t))
		entry := doc["artifacts"].([]any)[0].(map[string]any)
		if resourceUpdate {
			resources := []any{}
			for _, name := range []string{"a.txt", "b.txt"} {
				d := fixtureBytes([]byte(name), "text/plain")
				resources = append(resources, map[string]any{"name": name, "role": "metadata", "mediaType": d.MediaType, "size": d.Size, "digest": d.Digest, "bundlePath": "resources/" + name})
			}
			entry["resources"] = resources
			doc["uriCatalog"] = append(doc["uriCatalog"].([]any), map[string]any{"uri": "https://example.test/alias", "artifactRevision": "rev-1", "resource": "a.txt"})
		}
		sortComparisonCatalog(doc)
		before := comparisonBytes(t, doc)
		doc = comparisonDocument(t, versionedManifest(t, before, "2.0.0", digest(before)))
		if resourceUpdate {
			doc["uriCatalog"].([]any)[0].(map[string]any)["resource"] = "b.txt"
		} else {
			doc["storageCatalog"].([]any)[0].(map[string]any)["stores"] = []any{"data-center-inventory", "telemetry-store"}
		}
		after := comparisonBytes(t, doc)
		value := assertComparisonResponses(t, comparisonServer(t, before, after, map[string]model.Artifact{"rev-1": {Payload: fixtureBytes([]byte("true"), artifact.SchemaMediaType)}}))
		require.Equal(t, manifest.CompatibilityBreaking, value.Compatibility)
		if resourceUpdate {
			require.Len(t, value.URICatalog, 1)
			require.Equal(t, value.URICatalog[0].BeforeRevision, value.URICatalog[0].AfterRevision)
			require.Equal(t, "a.txt", value.URICatalog[0].BeforeResource)
			require.Equal(t, "b.txt", value.URICatalog[0].AfterResource)
		} else {
			require.Equal(t, manifest.ImpactBreaking, value.StorageCatalog[0].Impact)
		}
	}
}

// sortComparisonCatalog orders comparison fixtures' ASCII URI keys before canonicalization.
func sortComparisonCatalog(document map[string]any) {
	entries := document["uriCatalog"].([]any)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].(map[string]any)["uri"].(string) < entries[j].(map[string]any)["uri"].(string)
	})
}

// TestChangesMappingMetadataAndBehavior distinguishes serialization edits from condition changes
// without executing programs.
func TestChangesMappingMetadataAndBehavior(t *testing.T) {
	for _, behavior := range []bool{false, true} {
		doc := comparisonDocument(t, mustTestManifest(t))
		program := fixtureBytes([]byte("opaque"), "application/octet-stream")
		programRef := map[string]any{"programRef": "opaque.bin", "programDigest": program.Digest}
		descriptor := map[string]any{
			"$schema": "https://example.test/mapping-1.0.0", "formatVersion": "1.0.0", "id": "mapping",
			"sourceSchema": map[string]any{"id": "device", "revision": "rev-1"},
			"engine":       map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "opaque"},
			"programs":     map[string]any{"transformations": []any{map[string]any{"programRef": "opaque.bin", "programDigest": program.Digest, "cardinality": "one", "targetSchema": map[string]any{"id": "device", "revision": "rev-1"}}}},
		}
		payload, err := json.Marshal(descriptor)
		require.NoError(t, err)
		entry := map[string]any{
			"kind": "source-mapping", "artifactType": "mapping", "artifactRevision": "mapping-r1", "createdUnderSchemaVersion": "1.0.0", "targetSchemaVersion": "1.0.0",
			"canonicalUri": "https://registry.example.test/v1/resources/source-mapping/mapping/mapping-r1",
			"resources":    []any{map[string]any{"name": "opaque.bin", "role": "program", "mediaType": program.MediaType, "digest": program.Digest, "size": program.Size, "bundlePath": "programs/opaque.bin"}},
			"dependencies": []any{map[string]any{"kind": "schema", "artifactType": "device", "artifactRevision": "rev-1"}},
		}
		old := comparisonPayload(entry, payload, "application/vnd.apeiro.ocg.source-mapping.v1+json")
		doc["artifacts"] = append(doc["artifacts"].([]any), entry)
		doc["uriCatalog"] = append(doc["uriCatalog"].([]any), map[string]any{"uri": entry["canonicalUri"], "artifactRevision": "mapping-r1"})
		sortComparisonCatalog(doc)
		before := comparisonBytes(t, doc)
		doc = comparisonDocument(t, versionedManifest(t, before, "2.0.0", digest(before)))
		entry = doc["artifacts"].([]any)[1].(map[string]any)
		entry["artifactRevision"], entry["createdUnderSchemaVersion"] = "mapping-r2", "2.0.0"
		entry["canonicalUri"] = "https://registry.example.test/v1/resources/source-mapping/mapping/mapping-r2"
		freshPayload := append([]byte(" \n"), payload...)
		want := manifest.ImpactOperational
		if behavior {
			descriptor["programs"].(map[string]any)["condition"] = programRef
			freshPayload, err = json.Marshal(descriptor)
			require.NoError(t, err)
			want = manifest.ImpactBreaking
		}
		fresh := comparisonPayload(entry, freshPayload, "application/vnd.apeiro.ocg.source-mapping.v1+json")
		for _, raw := range doc["uriCatalog"].([]any) {
			u := raw.(map[string]any)
			if u["artifactRevision"] == "mapping-r1" {
				u["uri"], u["artifactRevision"] = entry["canonicalUri"], "mapping-r2"
			}
		}
		sortComparisonCatalog(doc)
		after := comparisonBytes(t, doc)
		value := assertComparisonResponses(t, comparisonServer(t, before, after, map[string]model.Artifact{
			"rev-1": {Payload: fixtureBytes([]byte("true"), artifact.SchemaMediaType)}, "mapping-r1": old, "mapping-r2": fresh,
		}))
		require.Len(t, value.Artifacts, 1)
		require.Equal(t, want, value.Artifacts[0].Impact)
		require.Equal(t, manifest.CompatibilityBreaking, value.Compatibility)
	}
}

// TestChangesAliasRequiresCompleteProof checks alias safety independently of target identity.
// Existing identifiers and unavailable supporting bytes prevent additive classification.
func TestChangesAliasRequiresCompleteProof(t *testing.T) {
	for _, target := range []string{"existing artifact", "new artifact"} {
		for _, proof := range []struct {
			name, payload string
			missing       bool
			want          manifest.Impact
		}{
			{"unused", "true", false, manifest.ImpactAdditive},
			{"existing identifier", `{"const":"https://example.test/alias"}`, false, manifest.ImpactBreaking},
			{"missing support bytes", "true", true, manifest.ImpactBreaking},
		} {
			t.Run(target+"/"+proof.name, func(t *testing.T) {
				doc := comparisonDocument(t, mustTestManifest(t))
				entry := doc["artifacts"].([]any)[0].(map[string]any)
				stored := comparisonPayload(entry, []byte(proof.payload), artifact.SchemaMediaType)
				if proof.missing {
					resource := fixtureBytes([]byte(`{"const":"https://example.test/alias"}`), "application/json")
					entry["resources"] = []any{map[string]any{
						"name": "support.json", "role": "support", "mediaType": resource.MediaType, "digest": resource.Digest, "size": resource.Size, "bundlePath": "support/schema.json",
					}}
				}
				before := comparisonBytes(t, doc)
				doc = comparisonDocument(t, versionedManifest(t, before, "2.0.0", digest(before)))
				artifacts := map[string]model.Artifact{"rev-1": stored}
				revision := "rev-1"
				if target == "new artifact" {
					revision = "location-r1"
					fresh := map[string]any{
						"kind": "schema", "artifactType": "location", "artifactRevision": revision, "createdUnderSchemaVersion": "2.0.0",
						"schemaClass": "source-record", "canonicalUri": "https://registry.example.test/v1/resources/schema/location/" + revision,
						"resources": []any{}, "dependencies": []any{},
					}
					artifacts[revision] = comparisonPayload(fresh, []byte("true"), artifact.SchemaMediaType)
					doc["artifacts"] = append(doc["artifacts"].([]any), fresh)
					doc["uriCatalog"] = append(doc["uriCatalog"].([]any), map[string]any{"uri": fresh["canonicalUri"], "artifactRevision": revision})
				}
				doc["uriCatalog"] = append(doc["uriCatalog"].([]any), map[string]any{"uri": "https://example.test/alias", "artifactRevision": revision})
				sortComparisonCatalog(doc)
				after := comparisonBytes(t, doc)
				value := assertComparisonResponses(t, comparisonServer(t, before, after, artifacts))
				require.NotEmpty(t, value.URICatalog)
				require.Equal(t, "https://example.test/alias", value.URICatalog[0].URI)
				require.Equal(t, proof.want, value.URICatalog[0].Impact)
				require.Equal(t, manifest.Compatibility(proof.want), value.Compatibility)
			})
		}
	}
}
