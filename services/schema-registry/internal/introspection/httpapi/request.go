// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
)

const (
	maxRequestTargetBytes = 16 * 1024
	maxHeaderFieldBytes   = 8 * 1024
	maxHeaderBytes        = 32 * 1024
	maxQueryValueBytes    = 8 * 1024
)

var errInvalidRequest = errors.New("invalid request")

// ValidateRequestMetadata rejects malformed and over-budget read requests
// before route-specific parsing or database admission.
func ValidateRequestMetadata(request *http.Request) error {
	if request == nil || len(request.RequestURI) > maxRequestTargetBytes {
		return errInvalidRequest
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return errInvalidRequest
	}
	if request.Body != nil && request.Body != http.NoBody {
		return errInvalidRequest
	}
	if len(request.TransferEncoding) > 0 {
		return errInvalidRequest
	}
	for _, contentEncoding := range request.Header.Values("Content-Encoding") {
		for _, coding := range strings.Split(contentEncoding, ",") {
			if coding = strings.TrimSpace(coding); coding != "" && !strings.EqualFold(coding, "identity") {
				return errInvalidRequest
			}
		}
	}
	total := 0
	for name, values := range request.Header {
		for _, value := range values {
			fieldSize := len(name) + len(value)
			if fieldSize > maxHeaderFieldBytes {
				return errInvalidRequest
			}
			total += fieldSize
			if total > maxHeaderBytes {
				return errInvalidRequest
			}
		}
	}
	return nil
}

// ValidateQuery permits exactly the named parameters and bounded decoded values.
func ValidateQuery(values url.Values, allowed ...string) error {
	permitted := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		permitted[name] = struct{}{}
	}
	for name, entries := range values {
		if _, ok := permitted[name]; !ok || len(entries) != 1 || entries[0] == "" || len(entries[0]) > maxQueryValueBytes {
			return errInvalidRequest
		}
	}
	return nil
}

// ParseAndValidateQuery rejects malformed percent escapes before validating the
// decoded values that route handlers consume.
func ParseAndValidateQuery(raw string, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, errInvalidRequest
	}
	if err := ValidateQuery(values, allowed...); err != nil {
		return nil, err
	}
	return values, nil
}

// ValidateEnum accepts only one of the route-defined values.
func ValidateEnum(value string, allowed ...string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return errInvalidRequest
}

// ValidateIdentifier checks a route identifier without accepting dot segments.
func ValidateIdentifier(value string) error {
	if !artifact.ValidRevision(value) {
		return errInvalidRequest
	}
	return nil
}

// ValidateResourceName checks a route resource name.
func ValidateResourceName(value string) error {
	if !artifact.ValidResourceName(value) {
		return errInvalidRequest
	}
	return nil
}
