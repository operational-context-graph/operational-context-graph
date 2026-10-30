// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/representation"
	"github.com/stretchr/testify/require"
)

func TestMappingHTTPUsesSharedRepresentationAndSize(t *testing.T) {
	for _, test := range []struct {
		name       string
		fixture    func() *artifactTestSnapshot
		collection string
	}{
		{"shared source and target", mappingFixture, "/v1/releases/1.0.0/schemas/device/source-mappings/packages"},
		{"mixed targets and shared programs", multiMappingFixture, candidateBase + "/packages"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := artifactTestServer(test.fixture())
			value, err := server.artifacts.MappingPackage(context.Background(), "1.0.0", "device-mapping")
			require.NoError(t, err)
			size, err := representation.MappingPackageSize(value.Value, "1.0.0")
			require.NoError(t, err)
			body, err := representation.BuildMappingPackage(value.Value, "1.0.0")
			require.NoError(t, err)
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			response := serveArtifactRequest(server, http.MethodGet, mappingBase+"/package")
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, encoded, response.Body.Bytes())
			require.EqualValues(t, response.Body.Len(), size)
			require.Equal(t, ETag(encoded), response.Header().Get("ETag"))
			collection := mappingJSON(t, serveArtifactRequest(server, http.MethodGet, test.collection+"?sourceMappingArtifactType=device-mapping"))
			var direct map[string]any
			require.NoError(t, json.Unmarshal(encoded, &direct))
			require.Equal(t, direct, collection["items"].([]any)[0])
		})
	}
}
