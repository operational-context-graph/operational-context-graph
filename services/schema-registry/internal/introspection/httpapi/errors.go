// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// ErrorCode is a stable protocol error identifier.
type ErrorCode string

const (
	InvalidRequest            ErrorCode = "invalid-request"
	InvalidQueryParameter     ErrorCode = "invalid-query-parameter"
	InvalidFilterCombination  ErrorCode = "invalid-filter-combination"
	UnsupportedArtifactKind   ErrorCode = "unsupported-artifact-kind"
	UnsupportedSchemaClass    ErrorCode = "unsupported-schema-class"
	InvalidCursor             ErrorCode = "invalid-cursor"
	VersionsNotComparable     ErrorCode = "versions-not-comparable"
	ReleaseNotFound           ErrorCode = "release-not-found"
	ArtifactNotInRelease      ErrorCode = "artifact-not-in-release"
	ArtifactNotFound          ErrorCode = "artifact-not-found"
	ResourceNotFound          ErrorCode = "resource-not-found"
	SchemaNotInRelease        ErrorCode = "schema-not-in-release"
	SourceMappingNotInRelease ErrorCode = "source-mapping-not-in-release"
	URINotInRelease           ErrorCode = "uri-not-in-release"
	LogicalArtifactNotFound   ErrorCode = "logical-artifact-not-found"
	NotAcceptable             ErrorCode = "not-acceptable"
	CursorExpired             ErrorCode = "cursor-expired"
	ReadLimitExceeded         ErrorCode = "read-limit-exceeded"
	InconsistentRegistryState ErrorCode = "inconsistent-registry-state"
	InternalError             ErrorCode = "internal-error"
	NoActiveRelease           ErrorCode = "no-active-release"
	RegistryUnavailable       ErrorCode = "registry-unavailable"
)

type errorDefinition struct {
	status    int
	message   string
	retryable bool
}

var errorCatalog = map[ErrorCode]errorDefinition{
	InvalidRequest: {400, "The request is malformed.", false}, InvalidQueryParameter: {400, "A query parameter is invalid.", false}, InvalidFilterCombination: {400, "The supplied filters cannot be combined.", false}, UnsupportedArtifactKind: {400, "The requested artifact kind is not supported.", false}, UnsupportedSchemaClass: {400, "The requested schema class is not supported.", false}, InvalidCursor: {400, "The cursor is invalid.", true}, VersionsNotComparable: {400, "The selected versions cannot be compared.", false},
	ReleaseNotFound: {404, "The selected release was not found.", true}, ArtifactNotInRelease: {404, "The selected release does not contain the requested artifact.", false}, ArtifactNotFound: {404, "The requested artifact was not found.", true}, ResourceNotFound: {404, "The requested resource was not found.", false}, SchemaNotInRelease: {404, "The selected release does not contain the requested schema.", false}, SourceMappingNotInRelease: {404, "The selected release does not contain the requested source mapping.", false}, URINotInRelease: {404, "The selected release does not contain the requested URI.", false}, LogicalArtifactNotFound: {404, "The requested logical artifact was not found.", true},
	NotAcceptable: {406, "No acceptable representation is available.", true}, CursorExpired: {410, "The cursor has expired.", true}, ReadLimitExceeded: {429, "The read limit has been exceeded.", true}, InconsistentRegistryState: {500, "The registry state is inconsistent.", true}, InternalError: {500, "An internal error occurred.", true}, NoActiveRelease: {503, "No active release is available.", true}, RegistryUnavailable: {503, "The registry is unavailable.", true},
}

// ErrorDetail supplies an optional stable validation detail.
type ErrorDetail struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}
type errorEnvelope struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code    ErrorCode     `json:"code"`
	Message string        `json:"message"`
	Target  string        `json:"target,omitempty"`
	Details []ErrorDetail `json:"details,omitempty"`
}

// WriteError emits a protocol-safe application error. Unknown codes retain a
// compatible envelope and map to an internal error without exposing causes.
func WriteError(writer http.ResponseWriter, code ErrorCode, target string, details []ErrorDetail, retryAfter time.Duration) {
	definition, ok := errorCatalog[code]
	if !ok {
		code, definition = InternalError, errorCatalog[InternalError]
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-cache")
	if retryAfter > 0 && (definition.status == http.StatusTooManyRequests || definition.status == http.StatusServiceUnavailable) {
		writer.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
	}
	writer.WriteHeader(definition.status)
	_ = json.NewEncoder(writer).Encode(errorEnvelope{Error: apiError{Code: code, Message: definition.message, Target: target, Details: details}})
}
