// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/manifest"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

func (s *Server) registerReleaseRoutes() {
	if s.releases == nil {
		return
	}
	for _, route := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{"/registry", s.registry},
		{"/releases", s.listReleases},
		{"/releases/:schemaVersion", s.releaseDescriptor},
		{"/releases/:schemaVersion/manifest", s.releaseManifest},
		{"/changes", s.changes},
	} {
		s.api.GET(route.path, route.handler)
		s.api.HEAD(route.path, route.handler)
	}
}

type links map[string]string
type registryResponse struct {
	SchemaVersion  string `json:"schemaVersion"`
	ManifestDigest string `json:"manifestDigest"`
	Links          links  `json:"links"`
}
type releaseResponse struct {
	SchemaVersion   string          `json:"schemaVersion"`
	ManifestDigest  string          `json:"manifestDigest"`
	PreviousRelease any             `json:"previousRelease"`
	Source          sourceResponse  `json:"source"`
	Compatibility   string          `json:"compatibility,omitempty"`
	Changes         json.RawMessage `json:"changes,omitempty"`
	Links           links           `json:"links"`
}
type sourceResponse struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}
type releaseCollection struct {
	SchemaVersion string            `json:"schemaVersion"`
	Items         []releaseResponse `json:"items"`
	NextCursor    *string           `json:"nextCursor"`
	Links         links             `json:"links"`
}
type changesResponse struct {
	FromSchemaVersion     string `json:"fromSchemaVersion"`
	ToSchemaVersion       string `json:"toSchemaVersion"`
	Compatibility         string `json:"compatibility"`
	Artifacts             any    `json:"artifacts"`
	SchemaContextBindings any    `json:"schemaContextBindings"`
	URICatalog            any    `json:"uriCatalog"`
	StorageCatalog        any    `json:"storageCatalog"`
	Links                 links  `json:"links"`
}

func (s *Server) registry(c *gin.Context) {
	value, err := s.releases.Discovery(c.Request.Context())
	if err != nil {
		s.writeReleaseError(c, err)
		return
	}
	version := value.Current.SchemaVersion
	body := registryResponse{version, value.Current.ManifestDigest, s.discoveryLinks(version)}
	s.writeJSON(c, http.StatusOK, version, "", body)
}

func defaultDiscoveryLinks(version string) links {
	return links{"self": "/v1/registry", "release": "/v1/releases/" + version, "manifest": "/v1/releases/" + version + "/manifest", "artifacts": "/v1/releases/" + version + "/artifacts", "schemas": "/v1/releases/" + version + "/schemas", "sourceMappings": "/v1/releases/" + version + "/source-mappings", "releases": "/v1/releases"}
}

func (s *Server) listReleases(c *gin.Context) {
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "limit", "cursor")
	if err != nil {
		s.writeReleaseValidationError(c, InvalidQueryParameter)
		return
	}
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			s.writeReleaseValidationError(c, InvalidQueryParameter)
			return
		}
	}
	var before, snapshot *int64
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: "releases", Limit: limit, Sort: "activation-desc", Filters: map[string]string{}})
		if decodeErr != nil {
			s.writeCursorError(c, decodeErr)
			return
		}
		last, parseErr := strconv.ParseInt(payload.Last, 10, 64)
		ceiling, ceilingErr := strconv.ParseInt(payload.Snapshot, 10, 64)
		if parseErr != nil || ceilingErr != nil || last <= 0 || ceiling <= 0 {
			s.writeReleaseValidationError(c, InvalidCursor)
			return
		}
		before, snapshot = &last, &ceiling
	}
	page, err := s.releases.List(c.Request.Context(), before, snapshot, limit+1)
	if err != nil {
		s.writeReleaseError(c, err)
		return
	}
	next := (*string)(nil)
	if len(page.Items) > limit {
		item := page.Items[limit-1]
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: "releases", Limit: limit, Sort: "activation-desc", Filters: map[string]string{}, Last: strconv.FormatInt(item.ActivationSequence, 10), Snapshot: strconv.FormatInt(page.Current.SnapshotSequence, 10)})
		if encodeErr != nil {
			s.writeReleaseError(c, postgres.ErrInconsistentState)
			return
		}
		next = &token
		page.Items = page.Items[:limit]
	}
	items := make([]releaseResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, releaseItem(item, false))
	}
	s.writeJSON(c, http.StatusOK, page.Current.SchemaVersion, "", releaseCollection{page.Current.SchemaVersion, items, next, links{"self": "/v1/releases"}})
}

func (s *Server) releaseDescriptor(c *gin.Context) {
	version := c.Param("schemaVersion")
	if !artifact.ValidVersion(version) {
		s.writeReleaseValidationError(c, InvalidRequest)
		return
	}
	value, err := s.releases.Get(c.Request.Context(), version)
	if err != nil {
		s.writeReleaseError(c, err)
		return
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, releaseItem(value.Release, true))
}

func (s *Server) releaseManifest(c *gin.Context) {
	version := c.Param("schemaVersion")
	if !artifact.ValidVersion(version) {
		s.writeReleaseValidationError(c, InvalidRequest)
		return
	}
	value, err := s.releases.Get(c.Request.Context(), version)
	if err != nil {
		s.writeReleaseError(c, err)
		return
	}
	if _, err = manifest.Parse(value.Release.ManifestBytes); err != nil {
		s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: value.Current.SchemaVersion, SelectedSchemaVersion: version})
		return
	}
	if _, ok := NegotiateMediaType(c.GetHeader("Accept"), manifest.MediaType); !ok {
		s.writeReleaseErrorResponse(c, NotAcceptable, 0, ErrorMetadata{SchemaVersion: value.Current.SchemaVersion, SelectedSchemaVersion: version})
		return
	}
	if err := WriteRepresentation(c.Writer, Representation{Status: http.StatusOK, MediaType: manifest.MediaType, Body: value.Release.ManifestBytes, Schema: value.Current.SchemaVersion, Selected: version, Digest: value.Release.ManifestDigest, DigestHeader: "Manifest-Digest", Method: c.Request.Method, AcceptCoding: c.GetHeader("Accept-Encoding"), Context: c.Request.Context(), IfNoneMatch: c.GetHeader("If-None-Match")}); err != nil && !c.Writer.Written() {
		s.writeReleaseRepresentationError(c, ErrorMetadata{SchemaVersion: value.Current.SchemaVersion, SelectedSchemaVersion: version})
	}
}

func (s *Server) changes(c *gin.Context) {
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "from", "to")
	if err != nil || values.Get("from") == "" || values.Get("to") == "" || !artifact.ValidVersion(values.Get("from")) || !artifact.ValidVersion(values.Get("to")) {
		s.writeReleaseValidationError(c, InvalidQueryParameter)
		return
	}
	changeSet, current, err := s.releases.Compare(c.Request.Context(), values.Get("from"), values.Get("to"))
	if err != nil {
		s.writeReleaseError(c, err)
		return
	}
	body := changesResponse{values.Get("from"), values.Get("to"), changeSet.Compatibility, changeSet.Artifacts, changeSet.SchemaContextBindings, changeSet.URICatalog, changeSet.StorageCatalog, links{"from": "/v1/releases/" + values.Get("from"), "to": "/v1/releases/" + values.Get("to")}}
	s.writeJSON(c, http.StatusOK, current.SchemaVersion, "", body)
}

func releaseItem(item model.Release, includeChanges bool) releaseResponse {
	var previous any
	if item.PreviousSchemaVersion != nil && item.PreviousManifestDigest != nil {
		previous = map[string]string{"schemaVersion": *item.PreviousSchemaVersion, "manifestDigest": *item.PreviousManifestDigest}
	}
	result := releaseResponse{SchemaVersion: item.SchemaVersion, ManifestDigest: item.ManifestDigest, PreviousRelease: previous, Source: sourceResponse{item.SourceRepository, item.SourceCommit}, Links: links{"self": "/v1/releases/" + item.SchemaVersion, "manifest": "/v1/releases/" + item.SchemaVersion + "/manifest", "artifacts": "/v1/releases/" + item.SchemaVersion + "/artifacts", "schemas": "/v1/releases/" + item.SchemaVersion + "/schemas", "sourceMappings": "/v1/releases/" + item.SchemaVersion + "/source-mappings"}}
	if len(item.Changes) > 0 {
		var changes struct {
			Compatibility string `json:"compatibility"`
		}
		if json.Unmarshal(item.Changes, &changes) == nil {
			result.Compatibility = changes.Compatibility
			if includeChanges {
				result.Changes = append(json.RawMessage(nil), item.Changes...)
			}
		}
	}
	return result
}

func (s *Server) writeJSON(c *gin.Context, status int, schema, selected string, value any) {
	if _, ok := NegotiateMediaType(c.GetHeader("Accept"), "application/json"); !ok {
		s.writeReleaseErrorResponse(c, NotAcceptable, 0, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
		return
	}
	if err := WriteRepresentation(c.Writer, Representation{Status: status, MediaType: "application/json", Body: body, Schema: schema, Selected: selected, Method: c.Request.Method, AcceptCoding: c.GetHeader("Accept-Encoding"), Context: c.Request.Context(), IfNoneMatch: c.GetHeader("If-None-Match")}); err != nil && !c.Writer.Written() {
		s.writeReleaseRepresentationError(c, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
	}
}

func (s *Server) writeReleaseRepresentationError(c *gin.Context, metadata ErrorMetadata) {
	s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, metadata)
}

func (s *Server) writeCursorError(c *gin.Context, err error) {
	s.writeReleaseValidationError(c, InvalidCursor)
}

func (s *Server) writeReleaseValidationError(c *gin.Context, code ErrorCode) {
	discovery, err := s.releases.Discovery(c.Request.Context())
	if err != nil {
		// Admission succeeded, so the current pointer determines the error for
		// every registry-dependent request. No active release takes precedence
		// over an application-level validation error.
		s.writeReleaseError(c, err)
		return
	}
	s.writeReleaseErrorResponse(c, code, 0, ErrorMetadata{SchemaVersion: discovery.Current.SchemaVersion})
}

func (s *Server) writeReleaseErrorResponse(c *gin.Context, code ErrorCode, retryAfter time.Duration, metadata ErrorMetadata) {
	if c.Request.Method == http.MethodHead {
		writeHeadError(c.Writer, code, "", nil, retryAfter, metadata)
		return
	}
	WriteErrorWithMetadata(c.Writer, code, "", nil, retryAfter, metadata)
}
func (s *Server) writeReleaseError(c *gin.Context, err error) {
	var code ErrorCode
	var retryAfter time.Duration
	switch {
	case errors.Is(err, postgres.ErrNoActiveRelease):
		code, retryAfter = NoActiveRelease, s.cfg.Admission.RetryInterval
	case errors.Is(err, postgres.ErrNotFound):
		code = ReleaseNotFound
	case errors.Is(err, release.ErrVersionsNotComparable):
		code = VersionsNotComparable
	case errors.Is(err, postgres.ErrInconsistentState):
		code = InconsistentRegistryState
	default:
		code, retryAfter = RegistryUnavailable, s.cfg.Admission.RetryInterval
	}
	metadata := ErrorMetadata{}
	if current, selected, ok := release.RequestMetadata(err); ok {
		metadata = ErrorMetadata{SchemaVersion: current.SchemaVersion, SelectedSchemaVersion: selected}
	}
	s.writeReleaseErrorResponse(c, code, retryAfter, metadata)
}
