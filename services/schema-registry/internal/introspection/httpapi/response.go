// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ETag returns the strong opaque validator for a selected representation.
func ETag(value []byte) string {
	sum := sha256.Sum256(value)
	return `"sha-256:` + hex.EncodeToString(sum[:]) + `"`
}

// ETagForRepresentation returns the validator for the selected bytes. Exact
// identity resources retain their stored digest when it is a valid SHA-256
// descriptor. All other representations use their selected bytes.
func ETagForRepresentation(value []byte, coding ContentCoding, digest string) string {
	if coding == Identity && validDigest(digest) {
		return `"` + digest + `"`
	}
	return ETag(value)
}

// RedirectETag returns the validator for a server-owned redirect target and
// the selected catalog entry that produced it.
func RedirectETag(location string, catalogEntry []byte) string {
	value := make([]byte, 0, len(location)+len(catalogEntry)+24)
	value = append(value, "ocg-redirect-v1\x00"...)
	value = strconv.AppendInt(value, int64(len(catalogEntry)), 10)
	value = append(value, '\x00')
	value = append(value, catalogEntry...)
	value = append(value, '\x00')
	value = append(value, location...)
	return ETag(value)
}

// MatchesIfNoneMatch evaluates a comma-separated If-None-Match field using
// weak comparison, as required for safe GET and HEAD revalidation.
func MatchesIfNoneMatch(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		candidate = strings.TrimPrefix(candidate, "W/")
		if validETag(candidate) && candidate == tag {
			return true
		}
	}
	return false
}

func validETag(tag string) bool {
	return len(tag) >= 2 && tag[0] == '"' && tag[len(tag)-1] == '"' && !strings.ContainsAny(tag[1:len(tag)-1], "\r\n\"")
}

func validDigest(digest string) bool {
	if len(digest) != len("sha-256:")+64 || !strings.HasPrefix(digest, "sha-256:") {
		return false
	}
	for _, value := range digest[len("sha-256:"):] {
		if value < '0' || value > '9' && value < 'a' || value > 'f' {
			return false
		}
	}
	return true
}

var ErrUnsafeResponseMetadata = errors.New("unsafe response metadata")

// Representation is a completely assembled response. WriteRepresentation
// never starts a successful response until encoding and validation finish.
type Representation struct {
	Status       int
	MediaType    string
	Body         []byte
	Schema       string
	Selected     string
	Digest       string
	Method       string
	AcceptCoding string
	Context      context.Context
	IfNoneMatch  string
}

// Redirect is a fully resolved server-owned redirect response.
type Redirect struct {
	Status       int
	Location     string
	Schema       string
	Selected     string
	IfNoneMatch  string
	CatalogEntry []byte
}

// RelativeLink creates a server-owned path from unescaped path segments.
// Callers MUST NOT provide a host, scheme, query, or already escaped segment.
func RelativeLink(segments ...string) (string, error) {
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || containsControl(segment) {
			return "", ErrUnsafeResponseMetadata
		}
		escaped[index] = escapePathSegment(segment)
	}
	return "/" + strings.Join(escaped, "/"), nil
}

func escapePathSegment(segment string) string {
	var builder strings.Builder
	for _, value := range []byte(segment) {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-._~", rune(value)) {
			builder.WriteByte(value)
			continue
		}
		_, _ = fmt.Fprintf(&builder, "%%%02X", value)
	}
	return builder.String()
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func WriteRepresentation(writer http.ResponseWriter, value Representation) error {
	if err := validateRepresentation(value); err != nil {
		return err
	}
	coding, ok := NegotiateCoding(value.AcceptCoding)
	if !ok {
		writer.Header().Set("Cache-Control", "no-cache")
		writer.WriteHeader(http.StatusNotAcceptable)
		return nil
	}
	ctx := value.Context
	if ctx == nil {
		ctx = context.Background()
	}
	body, err := EncodeRepresentationContext(ctx, value.Body, coding)
	if err != nil {
		return err
	}
	tag := ETagForRepresentation(body, coding, value.Digest)
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Vary", "Accept-Encoding")
	writer.Header().Set("ETag", tag)
	writer.Header().Set("Content-Type", value.MediaType)
	if value.Schema != "" {
		writer.Header().Set("Schema-Version", value.Schema)
	}
	if value.Selected != "" {
		writer.Header().Set("Selected-Schema-Version", value.Selected)
	}
	if value.Digest != "" {
		writer.Header().Set("Artifact-Digest", value.Digest)
	}
	if coding == Gzip {
		writer.Header().Set("Content-Encoding", "gzip")
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if MatchesIfNoneMatch(value.IfNoneMatch, tag) {
		writer.Header().Del("Content-Length")
		writer.WriteHeader(http.StatusNotModified)
		return nil
	}
	if value.Method == http.MethodHead {
		writer.WriteHeader(value.Status)
		return nil
	}
	writer.WriteHeader(value.Status)
	_, err = writer.Write(body)
	return err
}

// WriteRedirect writes a validated temporary redirect after all representation
// metadata has been assembled. It never derives Location from request headers.
func WriteRedirect(writer http.ResponseWriter, value Redirect) error {
	if value.Status == 0 {
		value.Status = http.StatusTemporaryRedirect
	}
	if value.Status != http.StatusTemporaryRedirect || !validRedirectLocation(value.Location) {
		return ErrUnsafeResponseMetadata
	}
	tag := RedirectETag(value.Location, value.CatalogEntry)
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("ETag", tag)
	if value.Schema != "" {
		writer.Header().Set("Schema-Version", value.Schema)
	}
	if value.Selected != "" {
		writer.Header().Set("Selected-Schema-Version", value.Selected)
	}
	if MatchesIfNoneMatch(value.IfNoneMatch, tag) {
		writer.WriteHeader(http.StatusNotModified)
		return nil
	}
	writer.Header().Set("Location", value.Location)
	writer.WriteHeader(value.Status)
	return nil
}

func validateRepresentation(value Representation) error {
	if value.Status < http.StatusOK || value.Status > 299 || value.Status == http.StatusNoContent || value.Status == http.StatusResetContent || value.MediaType == "" || containsControl(value.MediaType) || containsControl(value.Schema) || containsControl(value.Selected) || containsControl(value.Digest) {
		return ErrUnsafeResponseMetadata
	}
	if _, _, err := mime.ParseMediaType(value.MediaType); err != nil {
		return ErrUnsafeResponseMetadata
	}
	if value.Method != http.MethodGet && value.Method != http.MethodHead {
		return ErrUnsafeResponseMetadata
	}
	return nil
}

func validRedirectLocation(location string) bool {
	if location == "" || containsControl(location) || strings.HasPrefix(location, "//") || strings.Contains(location, "\\") {
		return false
	}
	parsed, err := url.ParseRequestURI(location)
	return err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && strings.HasPrefix(parsed.Path, "/")
}
