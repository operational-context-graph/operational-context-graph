// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRepresentationEncodesGzipAndSuppressesHeadBody(t *testing.T) {
	identity := []byte(`{"ok":true}`)
	digestSum := sha256.Sum256(identity)
	digest := "sha-256:" + hex.EncodeToString(digestSum[:])
	response := httptest.NewRecorder()
	err := WriteRepresentation(response, Representation{Status: http.StatusOK, MediaType: "application/json", Body: identity, Schema: "1.0.0", Digest: digest, Method: http.MethodGet, AcceptCoding: "gzip"})
	require.NoError(t, err)
	assert.Equal(t, "gzip", response.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", response.Header().Get("Vary"))
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Equal(t, digest, response.Header().Get("Artifact-Digest"))
	assert.NotEmpty(t, response.Header().Get("ETag"))
	assert.Equal(t, strconv.Itoa(response.Body.Len()), response.Header().Get("Content-Length"))
	plain, err := decompress(response.Body.Bytes())
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(plain))
	head := httptest.NewRecorder()
	require.NoError(t, WriteRepresentation(head, Representation{Status: http.StatusOK, MediaType: "application/json", Body: []byte(`{}`), Method: http.MethodHead, AcceptCoding: "gzip"}))
	assert.Empty(t, head.Body.String())
	assert.Equal(t, "gzip", head.Header().Get("Content-Encoding"))

	get := httptest.NewRecorder()
	require.NoError(t, WriteRepresentation(get, Representation{Status: http.StatusOK, MediaType: "application/json", Body: []byte(`{}`), Method: http.MethodGet, AcceptCoding: "gzip"}))
	assert.Equal(t, get.Header().Get("Content-Length"), head.Header().Get("Content-Length"))
	assert.Equal(t, get.Header().Get("ETag"), head.Header().Get("ETag"))
}

func TestWriteRepresentationReturnsBodyless406ForForbiddenCodings(t *testing.T) {
	response := httptest.NewRecorder()
	err := WriteRepresentation(response, Representation{
		Status:       http.StatusOK,
		MediaType:    "application/json",
		Body:         []byte(`{"ok":true}`),
		Method:       http.MethodGet,
		AcceptCoding: "identity;q=0, gzip;q=0",
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotAcceptable, response.Code)
	assert.Empty(t, response.Body.String())
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	assert.Empty(t, response.Header().Get("Content-Encoding"))
	assert.Empty(t, response.Header().Get("Vary"))
}

func TestWriteRepresentationDoesNotCommitAfterClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	err := WriteRepresentation(response, Representation{
		Status:       http.StatusOK,
		MediaType:    "application/json",
		Body:         []byte(`{"ok":true}`),
		Method:       http.MethodGet,
		AcceptCoding: "gzip",
		Context:      ctx,
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Empty(t, response.Header())
	assert.Empty(t, response.Body.String())
}

func TestWriteRepresentationStopsOnClientDisconnect(t *testing.T) {
	body := bytes.Repeat([]byte("x"), MaxIdentityRepresentationBytes)
	started := make(chan struct{})
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		finished <- WriteRepresentation(writer, Representation{
			Status:       http.StatusOK,
			MediaType:    "application/json",
			Body:         body,
			Method:       request.Method,
			AcceptCoding: request.Header.Get("Accept-Encoding"),
			Context:      request.Context(),
		})
	}))
	defer server.Close()

	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	_, err = connection.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\nAccept-Encoding: gzip\r\n\r\n"))
	require.NoError(t, err)
	<-started
	require.NoError(t, connection.Close())

	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("representation work did not stop after client disconnect")
	}
}

func TestValidatorsAreRepresentationSpecific(t *testing.T) {
	identity, err := EncodeRepresentation([]byte("body"), Identity)
	require.NoError(t, err)
	gzip, err := EncodeRepresentation([]byte("body"), Gzip)
	require.NoError(t, err)
	assert.NotEqual(t, ETag(identity), ETag(gzip))
	assert.True(t, MatchesIfNoneMatch(`"other", `+ETag(identity), ETag(identity)))
	assert.True(t, MatchesIfNoneMatch("*", ETag(identity)))
	assert.False(t, MatchesIfNoneMatch("malformed", ETag(identity)))
}

func TestETagUsesSelectedRepresentationAndExactIdentityDigest(t *testing.T) {
	identity := []byte(`{"items":[],"nextCursor":"opaque"}`)
	gzip, err := EncodeRepresentation(identity, Gzip)
	require.NoError(t, err)
	digestSum := sha256.Sum256(identity)
	digest := "sha-256:" + hex.EncodeToString(digestSum[:])

	identityTag := ETagForRepresentation(identity, Identity, digest)
	assert.Equal(t, `"`+digest+`"`, identityTag)
	assert.Equal(t, ETag(identity), ETagForRepresentation(identity, Identity, "not-a-digest"))
	assert.NotEqual(t, identityTag, ETagForRepresentation(gzip, Gzip, digest))
	assert.NotEqual(t, RedirectETag("/v1/releases/1.0.0", []byte("first")), RedirectETag("/v1/releases/1.0.0", []byte("second")))
}

func TestWriteRepresentationAppliesConditionalResponse(t *testing.T) {
	body := []byte(`{"items":[],"nextCursor":"stable-cursor"}`)
	first := httptest.NewRecorder()
	require.NoError(t, WriteRepresentation(first, Representation{
		Status: http.StatusOK, MediaType: "application/json", Body: body, Schema: "1.0.0", Method: http.MethodGet, AcceptCoding: "gzip",
	}))

	conditional := httptest.NewRecorder()
	require.NoError(t, WriteRepresentation(conditional, Representation{
		Status: http.StatusOK, MediaType: "application/json", Body: body, Schema: "2.0.0", Selected: "1.0.0", Method: http.MethodGet, AcceptCoding: "gzip", IfNoneMatch: first.Header().Get("ETag"),
	}))
	assert.Equal(t, http.StatusNotModified, conditional.Code)
	assert.Empty(t, conditional.Body.String())
	assert.Equal(t, first.Header().Get("ETag"), conditional.Header().Get("ETag"))
	assert.Equal(t, "2.0.0", conditional.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", conditional.Header().Get("Selected-Schema-Version"))
	assert.Equal(t, "Accept-Encoding", conditional.Header().Get("Vary"))
	assert.Equal(t, "no-cache", conditional.Header().Get("Cache-Control"))
	assert.Empty(t, conditional.Header().Get("Content-Length"))
}

func TestIfNoneMatchAcceptsListsWildcardsAndRejectsMalformedTags(t *testing.T) {
	tag := `"sha-256:abc"`
	assert.True(t, MatchesIfNoneMatch(`"other", W/`+tag, tag))
	assert.True(t, MatchesIfNoneMatch("*", tag))
	assert.False(t, MatchesIfNoneMatch(`W/sha-256:abc`, tag))
	assert.False(t, MatchesIfNoneMatch(`"unterminated`, tag))
}

func TestRelativeLinkIsServerOwnedAndEscapesEachSegmentOnce(t *testing.T) {
	link, err := RelativeLink("v1", "releases", "1.0.0", "resources", "name/%2f/%2e/✓")
	require.NoError(t, err)
	assert.Equal(t, "/v1/releases/1.0.0/resources/name%2F%252f%2F%252e%2F%E2%9C%93", link)
}

func TestRelativeLinkRejectsUnsafeSegments(t *testing.T) {
	for _, segment := range []string{"", ".", "..", "name\r\nLocation: https://invalid", "name\u0085"} {
		_, err := RelativeLink("v1", segment)
		assert.ErrorIs(t, err, ErrUnsafeResponseMetadata)
	}
}

func TestWriteRedirectIsConditionalAndDoesNotUseRequestAuthority(t *testing.T) {
	location, err := RelativeLink("v1", "releases", "1.0.0")
	require.NoError(t, err)
	response := httptest.NewRecorder()
	require.NoError(t, WriteRedirect(response, Redirect{Location: location, Schema: "2.0.0", Selected: "1.0.0", CatalogEntry: []byte("https://catalog.example/schema")}))
	assert.Equal(t, http.StatusTemporaryRedirect, response.Code)
	assert.Equal(t, "/v1/releases/1.0.0", response.Header().Get("Location"))
	assert.Equal(t, "2.0.0", response.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", response.Header().Get("Selected-Schema-Version"))
	assert.Empty(t, response.Body.String())

	conditional := httptest.NewRecorder()
	require.NoError(t, WriteRedirect(conditional, Redirect{Location: location, Schema: "3.0.0", CatalogEntry: []byte("https://catalog.example/schema"), IfNoneMatch: response.Header().Get("ETag")}))
	assert.Equal(t, http.StatusNotModified, conditional.Code)
	assert.Empty(t, conditional.Body.String())
	assert.Empty(t, conditional.Header().Get("Location"))
	assert.Equal(t, "3.0.0", conditional.Header().Get("Schema-Version"))
}

func TestWriteRepresentationDoesNotCommitOnLateFailure(t *testing.T) {
	response := httptest.NewRecorder()
	err := WriteRepresentation(response, Representation{
		Status: http.StatusOK, MediaType: "application/json", Body: make([]byte, MaxIdentityRepresentationBytes+1), Method: http.MethodGet,
	})
	require.ErrorIs(t, err, ErrRepresentationTooLarge)
	assert.Empty(t, response.Header())
	assert.Empty(t, response.Body.String())

	err = WriteRepresentation(response, Representation{
		Status: http.StatusOK, MediaType: "application/json\r\nX-Injected: true", Body: []byte("{}"), Method: http.MethodGet,
	})
	require.ErrorIs(t, err, ErrUnsafeResponseMetadata)
	assert.Empty(t, response.Header())
	assert.Empty(t, response.Body.String())
}

func TestWriteRedirectRejectsUnsafeLocationWithoutCommitting(t *testing.T) {
	for _, location := range []string{"/v1/releases/1.0.0\r\nLocation: https://invalid", "//attacker.example/path", "/\\attacker.example/path"} {
		response := httptest.NewRecorder()
		err := WriteRedirect(response, Redirect{Location: location})
		require.ErrorIs(t, err, ErrUnsafeResponseMetadata)
		assert.Empty(t, response.Header())
		assert.Empty(t, response.Body.String())
	}
}

func TestWriteRepresentationRejectsBodyForbiddenStatus(t *testing.T) {
	response := httptest.NewRecorder()
	err := WriteRepresentation(response, Representation{Status: http.StatusNoContent, MediaType: "application/json", Body: []byte("{}"), Method: http.MethodGet})
	require.ErrorIs(t, err, ErrUnsafeResponseMetadata)
	assert.Empty(t, response.Header())
	assert.Empty(t, response.Body.String())
}
