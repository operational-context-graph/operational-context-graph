package benchmark

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
)

func BenchmarkEncodeGzipHead64MiB(b *testing.B) {
	body := bytes.Repeat([]byte(`{"schemaVersion":"1.0.0","items":[]}`), (64*1024*1024)/36)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response := httptest.NewRecorder()
		err := httpapi.WriteRepresentation(response, httpapi.Representation{
			Status: http.StatusOK, MediaType: "application/json", Body: body,
			Method: http.MethodHead, AcceptCoding: "gzip",
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
	for b.Loop() {
		encoded, err := httpapi.EncodeRepresentation(body, httpapi.Identity)
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
	for b.Loop() {
		base64.StdEncoding.Encode(destination, payload)
	}
}

func BenchmarkHTTPHealthEndpoint(b *testing.B) {
	server := httpapi.New(introspection.Config{Admission: introspection.AdmissionConfig{MaxInFlight: 1}}, nil)
	request := httptest.NewRequest(http.MethodHead, "/healthz", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatal(response.Code)
		}
	}
}
