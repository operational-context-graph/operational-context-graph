// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

func (s *Server) registerArtifactRoutes() {
	if s.artifacts == nil {
		return
	}
	for _, route := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{"/releases/:schemaVersion/artifacts/:kind/:artifactType", s.releaseArtifact},
		{"/releases/:schemaVersion/artifacts", s.releaseArtifacts},
		{"/artifacts/:artifactRevision/metadata", s.artifactMetadata},
		{"/artifacts/:artifactRevision/resources/:resourceName", s.artifactResource},
		{"/artifacts/:artifactRevision", s.artifactPayload},
		{"/resources/:kind/:artifactType/:artifactRevision", s.canonicalResource},
		{"/releases/:schemaVersion/resolve", s.resolveURI},
		{"/artifact-kinds/:kind/:artifactType/history", s.artifactHistory},
	} {
		s.api.GET(route.path, route.handler)
		s.api.HEAD(route.path, route.handler)
	}
}

func (s *Server) releaseArtifacts(c *gin.Context) {
	version := c.Param("schemaVersion")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "kind", "artifactType", "schemaClass", "dependsOnRevision", "limit", "cursor")
	if !artifactmodel.ValidVersion(version) || err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	filter := model.ArtifactFilter{Kind: values.Get("kind"), ArtifactType: values.Get("artifactType"), SchemaClass: values.Get("schemaClass"), DependsOnRevision: values.Get("dependsOnRevision")}
	if filter.Kind != "" && !validKind(filter.Kind) {
		s.writeArtifactValidationError(c, UnsupportedArtifactKind)
		return
	}
	if filter.ArtifactType != "" && !artifactmodel.ValidLogicalType(filter.ArtifactType) {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	if filter.SchemaClass != "" && (filter.Kind != "schema" || !validSchemaClass(filter.SchemaClass)) {
		code := UnsupportedSchemaClass
		if filter.Kind != "schema" {
			code = InvalidFilterCombination
		}
		s.writeArtifactValidationError(c, code)
		return
	}
	if filter.DependsOnRevision != "" && !artifactmodel.ValidRevision(filter.DependsOnRevision) {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			s.writeArtifactValidationError(c, InvalidQueryParameter)
			return
		}
	}
	filters := map[string]string{"version": version, "kind": filter.Kind, "artifactType": filter.ArtifactType, "schemaClass": filter.SchemaClass, "dependsOnRevision": filter.DependsOnRevision}
	var afterKind, afterType *string
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: "release-artifacts", Limit: limit, Sort: "kind-type", Filters: filters})
		if decodeErr != nil {
			s.writeCursorError(c, decodeErr)
			return
		}
		parts := strings.Split(payload.Last, "\x00")
		if len(parts) != 2 || !validKind(parts[0]) || !artifactmodel.ValidLogicalType(parts[1]) {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		afterKind, afterType = &parts[0], &parts[1]
	}
	page, err := s.artifacts.List(c.Request.Context(), version, filter, afterKind, afterType, limit+1)
	if err != nil {
		s.writeArtifactError(c, err, ReleaseNotFound)
		return
	}
	next := (*string)(nil)
	if len(page.Items) > limit {
		last := page.Items[limit-1].Artifact
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: "release-artifacts", Limit: limit, Sort: "kind-type", Filters: filters, Last: last.Kind + "\x00" + last.Type, Snapshot: version})
		if encodeErr != nil {
			s.writeArtifactError(c, postgres.ErrInconsistentState, ReleaseNotFound)
			return
		}
		next = &token
		page.Items = page.Items[:limit]
	}
	items := make([]descriptorResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, descriptor(item, version))
	}
	s.writeJSON(c, http.StatusOK, page.Current.SchemaVersion, version, struct {
		SchemaVersion string               `json:"schemaVersion"`
		Items         []descriptorResponse `json:"items"`
		NextCursor    *string              `json:"nextCursor"`
		Links         links                `json:"links"`
	}{version, items, next, links{"self": "/v1/releases/" + version + "/artifacts"}})
}

func (s *Server) artifactHistory(c *gin.Context) {
	kind, artifactType := c.Param("kind"), c.Param("artifactType")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "limit", "cursor")
	if !validKind(kind) {
		s.writeArtifactValidationError(c, UnsupportedArtifactKind)
		return
	}
	if err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	if !artifactmodel.ValidLogicalType(artifactType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			s.writeArtifactValidationError(c, InvalidQueryParameter)
			return
		}
	}
	filters := map[string]string{"kind": kind, "artifactType": artifactType}
	var before, snapshotSequence *int64
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: "artifact-history", Limit: limit, Sort: "activation-desc", Filters: filters})
		if decodeErr != nil {
			s.writeCursorError(c, decodeErr)
			return
		}
		sequence, parseErr := strconv.ParseInt(payload.Last, 10, 64)
		if parseErr != nil || sequence <= 0 {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		before = &sequence
		ceiling, ceilingErr := strconv.ParseInt(payload.Snapshot, 10, 64)
		if ceilingErr != nil || ceiling <= 0 {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		snapshotSequence = &ceiling
	}
	result, err := s.artifacts.History(c.Request.Context(), kind, artifactType, before, snapshotSequence, limit+1)
	if err != nil {
		s.writeArtifactError(c, err, LogicalArtifactNotFound)
		return
	}
	next := (*string)(nil)
	items := result.Value
	if len(items) > limit {
		last := items[limit-1]
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: "artifact-history", Limit: limit, Sort: "activation-desc", Filters: filters, Last: strconv.FormatInt(last.ActivationSequence, 10), Snapshot: strconv.FormatInt(result.Current.SnapshotSequence, 10)})
		if encodeErr != nil {
			s.writeArtifactError(c, postgres.ErrInconsistentState, LogicalArtifactNotFound)
			return
		}
		next = &token
		items = items[:limit]
	}
	type historyItem struct {
		SchemaVersion             string `json:"schemaVersion"`
		ArtifactRevision          string `json:"artifactRevision"`
		CreatedUnderSchemaVersion string `json:"createdUnderSchemaVersion"`
		Links                     links  `json:"links"`
	}
	responseItems := make([]historyItem, 0, len(items))
	for _, item := range items {
		descriptorResult, descriptorErr := s.artifacts.GetArtifact(c.Request.Context(), item.ArtifactRevision)
		if descriptorErr != nil {
			s.writeArtifactError(c, descriptorErr, LogicalArtifactNotFound)
			return
		}
		responseItems = append(responseItems, historyItem{item.SchemaVersion, item.ArtifactRevision, descriptorResult.Value.CreatedUnderSchemaVersion, links{"artifact": "/v1/releases/" + item.SchemaVersion + "/artifacts/" + kind + "/" + artifactType, "metadata": "/v1/artifacts/" + item.ArtifactRevision + "/metadata"}})
	}
	s.writeJSON(c, http.StatusOK, result.Current.SchemaVersion, "", struct {
		SchemaVersion string        `json:"schemaVersion"`
		Items         []historyItem `json:"items"`
		NextCursor    *string       `json:"nextCursor"`
		Links         links         `json:"links"`
	}{result.Current.SchemaVersion, responseItems, next, links{"self": "/v1/artifact-kinds/" + kind + "/" + artifactType + "/history"}})
}

type byteResponse struct {
	MediaType    string
	Digest       string
	Bytes        []byte
	Revision     string
	ResourceName string
}
type descriptorResponse struct {
	Kind                      string               `json:"kind"`
	ArtifactType              string               `json:"artifactType"`
	ArtifactRevision          string               `json:"artifactRevision"`
	CreatedUnderSchemaVersion string               `json:"createdUnderSchemaVersion"`
	SchemaClass               *string              `json:"schemaClass,omitempty"`
	TargetSchemaVersion       *string              `json:"targetSchemaVersion,omitempty"`
	CanonicalURI              string               `json:"canonicalUri"`
	Payload                   payloadResponse      `json:"payload"`
	Resources                 []resourceResponse   `json:"resources"`
	Dependencies              []dependencyResponse `json:"dependencies"`
	Extensions                json.RawMessage      `json:"extensions,omitempty"`
	Links                     links                `json:"links"`
}
type payloadResponse struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}
type resourceResponse struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}
type dependencyResponse struct {
	Kind             string `json:"kind"`
	ArtifactType     string `json:"artifactType"`
	ArtifactRevision string `json:"artifactRevision"`
	Href             string `json:"href"`
}

func validKind(value string) bool {
	return value == "schema" || value == "context" || value == "source-mapping"
}

func (s *Server) releaseArtifact(c *gin.Context) {
	version, kind, artifactType := c.Param("schemaVersion"), c.Param("kind"), c.Param("artifactType")
	if !artifactmodel.ValidVersion(version) || !validKind(kind) || !artifactmodel.ValidLogicalType(artifactType) {
		s.writeArtifactValidationError(c, invalidArtifactPathCode(kind))
		return
	}
	value, err := s.artifacts.ReleaseDescriptor(c.Request.Context(), version, kind, artifactType)
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotInRelease)
		return
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, descriptor(value.Value, version))
}

func (s *Server) artifactMetadata(c *gin.Context) {
	revision := c.Param("artifactRevision")
	if !artifactmodel.ValidRevision(revision) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.ImmutableDescriptor(c.Request.Context(), revision)
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotFound)
		return
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, "", descriptor(value.Value, ""))
}

func (s *Server) artifactPayload(c *gin.Context) {
	revision := c.Param("artifactRevision")
	if !artifactmodel.ValidRevision(revision) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.GetArtifact(c.Request.Context(), revision)
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotFound)
		return
	}
	s.writeArtifactBytes(c, value.Current.SchemaVersion, "", byteResponse{value.Value.Payload.MediaType, value.Value.Payload.Digest, value.Value.Payload.Bytes, value.Value.Revision, ""})
}

func (s *Server) artifactResource(c *gin.Context) {
	revision, name := c.Param("artifactRevision"), c.Param("resourceName")
	if !artifactmodel.ValidRevision(revision) || !artifactmodel.ValidResourceName(name) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if _, err := s.artifacts.GetArtifact(c.Request.Context(), revision); err != nil {
		s.writeArtifactError(c, err, ArtifactNotFound)
		return
	}
	value, err := s.artifacts.GetResource(c.Request.Context(), revision, name)
	if err != nil {
		s.writeArtifactError(c, err, ResourceNotFound)
		return
	}
	if err := artifact.EnsureResourceOwner(value.Value, revision); err != nil {
		s.writeArtifactError(c, err, ResourceNotFound)
		return
	}
	s.writeArtifactBytes(c, value.Current.SchemaVersion, "", byteResponse{value.Value.MediaType, value.Value.Digest, value.Value.Bytes, revision, value.Value.Name})
}

func (s *Server) canonicalResource(c *gin.Context) {
	kind, artifactType, revision := c.Param("kind"), c.Param("artifactType"), c.Param("artifactRevision")
	if !validKind(kind) {
		s.writeArtifactValidationError(c, UnsupportedArtifactKind)
		return
	}
	if !artifactmodel.ValidLogicalType(artifactType) || !artifactmodel.ValidRevision(revision) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.GetArtifact(c.Request.Context(), revision)
	if err == nil {
		err = artifact.ValidateCanonical(value.Value, kind, artifactType, revision)
	}
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotFound)
		return
	}
	s.writeArtifactBytes(c, value.Current.SchemaVersion, "", byteResponse{value.Value.Payload.MediaType, value.Value.Payload.Digest, value.Value.Payload.Bytes, revision, ""})
}

func (s *Server) resolveURI(c *gin.Context) {
	version := c.Param("schemaVersion")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "uri")
	if !artifactmodel.ValidVersion(version) || err != nil || values.Get("uri") == "" {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	value, err := s.artifacts.ResolveURI(c.Request.Context(), version, values.Get("uri"))
	if err != nil {
		s.writeArtifactError(c, err, URINotInRelease)
		return
	}
	segments := []string{"v1", "artifacts", value.Value.ArtifactRevision}
	if value.Value.ResourceName != nil {
		segments = append(segments, "resources", *value.Value.ResourceName)
	}
	location, err := RelativeLink(segments...)
	if err != nil {
		s.writeArtifactError(c, postgres.ErrInconsistentState, URINotInRelease)
		return
	}
	catalog, _ := json.Marshal(value.Value)
	if err := WriteRedirect(c.Writer, Redirect{Location: location, Schema: value.Current.SchemaVersion, Selected: version, IfNoneMatch: c.GetHeader("If-None-Match"), CatalogEntry: catalog}); err != nil && !c.Writer.Written() {
		s.writeArtifactError(c, err, URINotInRelease)
	}
}

func (s *Server) writeArtifactBytes(c *gin.Context, schema, selected string, value byteResponse) {
	if _, ok := NegotiateMediaType(c.GetHeader("Accept"), value.MediaType); !ok {
		s.writeReleaseErrorResponse(c, NotAcceptable, 0, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
		return
	}
	c.Writer.Header().Set("Artifact-Revision", value.Revision)
	if value.ResourceName != "" {
		c.Writer.Header().Set("Resource-Name", value.ResourceName)
	}
	if err := WriteRepresentation(c.Writer, Representation{Status: http.StatusOK, MediaType: value.MediaType, Body: value.Bytes, Schema: schema, Selected: selected, Digest: value.Digest, Method: c.Request.Method, AcceptCoding: c.GetHeader("Accept-Encoding"), Context: c.Request.Context(), IfNoneMatch: c.GetHeader("If-None-Match")}); err != nil && !c.Writer.Written() {
		s.writeArtifactError(c, err, ArtifactNotFound)
		return
	}
}

func descriptor(value artifact.Descriptor, selected string) descriptorResponse {
	item := value.Artifact
	result := descriptorResponse{Kind: item.Kind, ArtifactType: item.Type, ArtifactRevision: item.Revision, CreatedUnderSchemaVersion: item.CreatedUnderSchemaVersion, SchemaClass: item.SchemaClass, TargetSchemaVersion: item.TargetSchemaVersion, CanonicalURI: item.CanonicalURI, Payload: payloadResponse{item.Payload.MediaType, item.Payload.Digest, item.Payload.Size, "/v1/artifacts/" + item.Revision}, Resources: make([]resourceResponse, 0, len(value.Resources)), Dependencies: make([]dependencyResponse, 0, len(value.Dependencies)), Links: links{"content": "/v1/artifacts/" + item.Revision, "history": "/v1/artifact-kinds/" + item.Kind + "/" + item.Type + "/history"}}
	if selected == "" {
		result.Links["self"] = "/v1/artifacts/" + item.Revision + "/metadata"
	} else {
		result.Links["self"] = "/v1/releases/" + selected + "/artifacts/" + item.Kind + "/" + item.Type
	}
	if len(item.Extensions) > 0 {
		result.Extensions = append(json.RawMessage(nil), item.Extensions...)
	}
	for _, resource := range value.Resources {
		result.Resources = append(result.Resources, resourceResponse{resource.Name, resource.Role, resource.MediaType, resource.Digest, resource.Size, "/v1/artifacts/" + item.Revision + "/resources/" + resource.Name})
	}
	for _, dependency := range value.Dependencies {
		href := "/v1/artifacts/" + dependency.Revision + "/metadata"
		if selected != "" {
			href = "/v1/releases/" + selected + "/artifacts/" + dependency.Kind + "/" + dependency.ArtifactType
		}
		result.Dependencies = append(result.Dependencies, dependencyResponse{dependency.Kind, dependency.ArtifactType, dependency.Revision, href})
	}
	if selected != "" && item.Kind == "schema" {
		base := "/v1/releases/" + selected + "/schemas/" + item.Type
		result.Links["contexts"], result.Links["package"], result.Links["sourceMapping"], result.Links["stores"] = base+"/contexts", base+"/package", base+"/source-mapping", base+"/stores"
	}
	if selected != "" && item.Kind == "source-mapping" {
		result.Links["package"] = "/v1/releases/" + selected + "/schemas/" + value.SourceArtifactType + "/source-mapping/package"
	}
	return result
}

func invalidArtifactPathCode(kind string) ErrorCode {
	if !validKind(kind) {
		return UnsupportedArtifactKind
	}
	return InvalidRequest
}

func validSchemaClass(value string) bool {
	switch value {
	case "entity-type", "identifier-type", "edge-type", "source-record":
		return true
	default:
		return false
	}
}

func (s *Server) writeArtifactValidationError(c *gin.Context, code ErrorCode) {
	s.writeReleaseValidationError(c, code)
}

func (s *Server) writeArtifactError(c *gin.Context, err error, notFound ErrorCode) {
	var code ErrorCode
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		code = notFound
	case errors.Is(err, postgres.ErrInconsistentState):
		code = InconsistentRegistryState
	default:
		s.writeReleaseError(c, err)
		return
	}
	current, selected, ok := release.RequestMetadata(err)
	metadata := ErrorMetadata{}
	if ok {
		metadata = ErrorMetadata{SchemaVersion: current.SchemaVersion, SelectedSchemaVersion: selected}
	}
	s.writeReleaseErrorResponse(c, code, 0, metadata)
}
