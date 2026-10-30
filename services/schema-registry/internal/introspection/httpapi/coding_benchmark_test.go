// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

func BenchmarkEncodeGzipHead64MiB(b *testing.B) {
	body := bytes.Repeat([]byte(`{"schemaVersion":"1.0.0","items":[]}`), int(config.MaxGeneratedJSONBytes)/36)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response := httptest.NewRecorder()
		err := WriteRepresentation(response, Representation{
			Status:       http.StatusOK,
			MediaType:    "application/json",
			Body:         body,
			Method:       http.MethodHead,
			AcceptCoding: "gzip",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeIdentityExact16MiB(b *testing.B) {
	body := bytes.Repeat([]byte("x"), 16*1024*1024)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		encoded, err := EncodeRepresentation(body, Identity)
		if err != nil {
			b.Fatal(err)
		}
		_ = encoded
	}
}

func BenchmarkPackageBase64Expansion(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 16*1024*1024)
	destination := make([]byte, base64.StdEncoding.EncodedLen(len(payload)))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		base64.StdEncoding.Encode(destination, payload)
	}
}
