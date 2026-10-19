// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRequestMetadata(t *testing.T) {
	valid := httptest.NewRequest(http.MethodGet, "/v1/releases", nil)
	assert.NoError(t, ValidateRequestMetadata(valid))

	cases := []struct {
		name    string
		request *http.Request
	}{
		{"target one over", httptest.NewRequest(http.MethodGet, "/"+strings.Repeat("a", maxRequestTargetBytes), nil)},
		{"body", httptest.NewRequest(http.MethodGet, "/v1/releases", strings.NewReader("x"))},
		{"transfer coding", requestWithTransferCoding()},
		{"content coding", requestWithHeader("Content-Encoding", "gzip")},
		{"unknown length body", requestWithUnknownLengthBody()},
		{"second content coding", requestWithSecondContentCoding()},
		{"field one over", requestWithHeader("X-Test", strings.Repeat("a", maxHeaderFieldBytes))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) { assert.ErrorIs(t, ValidateRequestMetadata(test.request), errInvalidRequest) })
	}
}

func TestValidateQueryAndIdentifiers(t *testing.T) {
	assert.NoError(t, ValidateQuery(url.Values{"limit": {"1000"}, "cursor": {strings.Repeat("a", maxQueryValueBytes)}}, "limit", "cursor"))
	assert.Error(t, ValidateQuery(url.Values{"unknown": {"x"}}, "limit"))
	assert.Error(t, ValidateQuery(url.Values{"limit": {"", "1"}}, "limit"))
	assert.Error(t, ValidateQuery(url.Values{"cursor": {strings.Repeat("a", maxQueryValueBytes+1)}}, "cursor"))
	_, err := ParseAndValidateQuery("cursor=%zz", "cursor")
	assert.ErrorIs(t, err, errInvalidRequest)
	assert.NoError(t, ValidateEnum("schema", "schema", "context"))
	assert.ErrorIs(t, ValidateEnum("unknown", "schema", "context"), errInvalidRequest)
	assert.NoError(t, ValidateIdentifier(strings.Repeat("a", 255)))
	assert.Error(t, ValidateIdentifier(".."))
	assert.Error(t, ValidateResourceName("a/b"))
}

func requestWithHeader(name, value string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/releases", nil)
	request.Header.Set(name, value)
	return request
}

func requestWithTransferCoding() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/releases", nil)
	request.TransferEncoding = []string{"chunked"}
	return request
}

func requestWithUnknownLengthBody() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/releases", nil)
	request.Body = io.NopCloser(strings.NewReader("body"))
	request.ContentLength = -1
	return request
}

func requestWithSecondContentCoding() *http.Request {
	request := requestWithHeader("Content-Encoding", "identity")
	request.Header.Add("Content-Encoding", "gzip")
	return request
}
