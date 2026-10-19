// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorCatalogProducesStableSafeEnvelopes(t *testing.T) {
	for code, definition := range errorCatalog {
		t.Run(string(code), func(t *testing.T) {
			response := httptest.NewRecorder()
			WriteError(response, code, "/v1/releases/1.0.0", nil, time.Second)
			assert.Equal(t, definition.status, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
			if definition.status == http.StatusTooManyRequests || definition.status == http.StatusServiceUnavailable {
				assert.Equal(t, "1", response.Header().Get("Retry-After"))
			}
			var envelope errorEnvelope
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			assert.Equal(t, code, envelope.Error.Code)
			assert.Equal(t, definition.message, envelope.Error.Message)
			assert.NotContains(t, response.Body.String(), "password")
			assert.NotContains(t, response.Body.String(), "stack")
		})
	}
}

func TestWriteErrorMapsUnknownCodeWithoutCauseDisclosure(t *testing.T) {
	response := httptest.NewRecorder()
	WriteError(response, "database password SELECT stack trace", "", nil, 0)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "database password")
	assert.NotContains(t, response.Body.String(), "stack trace")
}
