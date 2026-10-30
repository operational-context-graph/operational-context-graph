// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/stretchr/testify/require"
)

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
