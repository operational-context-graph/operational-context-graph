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
	URINotInRelease           ErrorCode = "uri-not-in-release"
	LogicalArtifactNotFound   ErrorCode = "logical-artifact-not-found"
	NotAcceptable             ErrorCode = "not-acceptable"
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

func errorDefinitionFor(code ErrorCode) (errorDefinition, bool) {
	switch code {
	case InvalidRequest:
		return errorDefinition{http.StatusBadRequest, "The request is malformed.", false}, true
	case InvalidQueryParameter:
		return errorDefinition{http.StatusBadRequest, "A query parameter is invalid.", false}, true
	case InvalidFilterCombination:
		return errorDefinition{http.StatusBadRequest, "The supplied filters cannot be combined.", false}, true
	case UnsupportedArtifactKind:
		return errorDefinition{http.StatusBadRequest, "The requested artifact kind is not supported.", false}, true
	case UnsupportedSchemaClass:
		return errorDefinition{http.StatusBadRequest, "The requested schema class is not supported.", false}, true
	case InvalidCursor:
		return errorDefinition{http.StatusBadRequest, "The cursor is invalid.", true}, true
	case VersionsNotComparable:
		return errorDefinition{http.StatusBadRequest, "The selected versions cannot be compared.", false}, true
	case ReleaseNotFound:
		return errorDefinition{http.StatusNotFound, "The selected release was not found.", true}, true
	case ArtifactNotInRelease:
		return errorDefinition{http.StatusNotFound, "The selected release does not contain the requested artifact.", false}, true
	case ArtifactNotFound:
		return errorDefinition{http.StatusNotFound, "The requested artifact was not found.", true}, true
	case ResourceNotFound:
		return errorDefinition{http.StatusNotFound, "The requested resource was not found.", false}, true
	case SchemaNotInRelease:
		return errorDefinition{http.StatusNotFound, "The selected release does not contain the requested schema.", false}, true
	case URINotInRelease:
		return errorDefinition{http.StatusNotFound, "The selected release does not contain the requested URI.", false}, true
	case LogicalArtifactNotFound:
		return errorDefinition{http.StatusNotFound, "The requested logical artifact was not found.", true}, true
	case NotAcceptable:
		return errorDefinition{http.StatusNotAcceptable, "No acceptable representation is available.", true}, true
	case ReadLimitExceeded:
		return errorDefinition{http.StatusTooManyRequests, "The read limit has been exceeded.", true}, true
	case InconsistentRegistryState:
		return errorDefinition{http.StatusInternalServerError, "The registry state is inconsistent.", true}, true
	case InternalError:
		return errorDefinition{http.StatusInternalServerError, "An internal error occurred.", true}, true
	case NoActiveRelease:
		return errorDefinition{http.StatusServiceUnavailable, "No active release is available.", true}, true
	case RegistryUnavailable:
		return errorDefinition{http.StatusServiceUnavailable, "The registry is unavailable.", true}, true
	default:
		return errorDefinition{}, false
	}
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

// ErrorMetadata contains already-resolved request snapshot headers. Callers
// MUST leave both values empty for errors emitted before database admission.
type ErrorMetadata struct {
	SchemaVersion         string
	SelectedSchemaVersion string
}

// WriteError emits a protocol-safe application error. Unknown codes retain a
// compatible envelope and map to an internal error without exposing causes.
func WriteError(writer http.ResponseWriter, code ErrorCode, target string, details []ErrorDetail, retryAfter time.Duration) {
	WriteErrorWithMetadata(writer, code, target, details, retryAfter, ErrorMetadata{})
}

// WriteErrorWithMetadata emits an error without losing a safely resolved read
// snapshot. It does not accept arbitrary headers or untrusted values.
func WriteErrorWithMetadata(writer http.ResponseWriter, code ErrorCode, target string, details []ErrorDetail, retryAfter time.Duration, metadata ErrorMetadata) {
	writeError(writer, code, target, details, retryAfter, metadata, true)
}

func writeHeadError(writer http.ResponseWriter, code ErrorCode, target string, details []ErrorDetail, retryAfter time.Duration, metadata ErrorMetadata) {
	writeError(writer, code, target, details, retryAfter, metadata, false)
}

func writeError(writer http.ResponseWriter, code ErrorCode, target string, details []ErrorDetail, retryAfter time.Duration, metadata ErrorMetadata, includeBody bool) {
	definition, ok := errorDefinitionFor(code)
	if !ok {
		code = InternalError
		definition, _ = errorDefinitionFor(InternalError)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-cache")
	if metadata.SchemaVersion != "" {
		writer.Header().Set("Schema-Version", metadata.SchemaVersion)
	}
	if metadata.SelectedSchemaVersion != "" {
		writer.Header().Set("Selected-Schema-Version", metadata.SelectedSchemaVersion)
	}
	if retryAfter > 0 && (definition.status == http.StatusTooManyRequests || definition.status == http.StatusServiceUnavailable) {
		writer.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
	}
	writer.WriteHeader(definition.status)
	if includeBody {
		_ = json.NewEncoder(writer).Encode(errorEnvelope{Error: apiError{Code: code, Message: definition.message, Target: target, Details: details}})
	}
}
