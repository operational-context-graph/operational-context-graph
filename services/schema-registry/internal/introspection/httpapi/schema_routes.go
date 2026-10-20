// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

const maxGeneratedJSONBytes = 64 << 20

func (s *Server) registerSchemaRoutes() {
	if s.artifacts == nil {
		return
	}
	for _, route := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{"/releases/:schemaVersion/schemas/:artifactType/source-mapping/package", s.mappingPackage},
		{"/releases/:schemaVersion/schemas/:artifactType/source-mapping", s.schemaMapping},
		{"/releases/:schemaVersion/schemas/:artifactType/contexts", s.schemaContexts},
		{"/releases/:schemaVersion/schemas/:artifactType/package", s.schemaPackage},
		{"/releases/:schemaVersion/schemas/:artifactType/content", s.schemaContent},
		{"/releases/:schemaVersion/schemas/:artifactType/stores", s.schemaStores},
		{"/releases/:schemaVersion/schemas/:artifactType", s.schemaMetadata},
		{"/releases/:schemaVersion/schemas", s.schemas},
		{"/releases/:schemaVersion/source-mappings", s.mappings},
	} {
		s.api.GET(route.path, route.handler)
		s.api.HEAD(route.path, route.handler)
	}
}

func validSchemaPath(version, artifactType string) bool {
	return artifactmodel.ValidVersion(version) && artifactmodel.ValidLogicalType(artifactType)
}

func (s *Server) schemas(c *gin.Context) {
	version := c.Param("schemaVersion")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "schemaClass", "dependsOnRevision", "limit", "cursor")
	if !artifactmodel.ValidVersion(version) || err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	filter := model.ArtifactFilter{Kind: "schema", SchemaClass: values.Get("schemaClass"), DependsOnRevision: values.Get("dependsOnRevision")}
	if filter.SchemaClass != "" && !validSchemaClass(filter.SchemaClass) {
		s.writeArtifactValidationError(c, UnsupportedSchemaClass)
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
	filters := map[string]string{"version": version, "schemaClass": filter.SchemaClass, "dependsOnRevision": filter.DependsOnRevision}
	var afterType *string
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: "release-schemas", Limit: limit, Sort: "artifact-type", Filters: filters})
		if decodeErr != nil || !artifactmodel.ValidLogicalType(payload.Last) {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		afterType = &payload.Last
	}
	var afterKind *string
	if afterType != nil {
		kind := "schema"
		afterKind = &kind
	}
	page, err := s.artifacts.List(c.Request.Context(), version, filter, afterKind, afterType, limit+1)
	if err != nil {
		s.writeArtifactError(c, err, ReleaseNotFound)
		return
	}
	next := (*string)(nil)
	if len(page.Items) > limit {
		last := page.Items[limit-1].Artifact.Type
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: "release-schemas", Limit: limit, Sort: "artifact-type", Filters: filters, Last: last, Snapshot: version})
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
	}{version, items, next, links{"self": "/v1/releases/" + version + "/schemas"}})
}

func (s *Server) schemaMetadata(c *gin.Context) {
	version, artifactType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, artifactType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.ReleaseDescriptor(c.Request.Context(), version, "schema", artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, descriptor(value.Value, version))
}

func (s *Server) schemaContent(c *gin.Context) {
	version, artifactType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, artifactType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.GetReleaseArtifact(c.Request.Context(), version, "schema", artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	if value.Value.Payload.MediaType != "application/schema+json" {
		s.writeArtifactError(c, postgres.ErrInconsistentState, SchemaNotInRelease)
		return
	}
	digest := sha256.Sum256(value.Value.Payload.Bytes)
	if int64(len(value.Value.Payload.Bytes)) != value.Value.Payload.Size || "sha-256:"+hex.EncodeToString(digest[:]) != value.Value.Payload.Digest {
		s.writeArtifactError(c, postgres.ErrInconsistentState, SchemaNotInRelease)
		return
	}
	s.writeArtifactBytes(c, value.Current.SchemaVersion, version, byteResponse{value.Value.Payload.MediaType, value.Value.Payload.Digest, value.Value.Payload.Bytes, value.Value.Revision, ""})
}

func (s *Server) schemaContexts(c *gin.Context) {
	version, artifactType := c.Param("schemaVersion"), c.Param("artifactType")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery)
	if !validSchemaPath(version, artifactType) || err != nil || len(values) != 0 {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	value, err := s.artifacts.SchemaPackage(c.Request.Context(), version, artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	items := make([]descriptorResponse, 0, len(value.Value.Contexts))
	for _, item := range value.Value.Contexts {
		items = append(items, descriptor(item, version))
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, struct {
		SchemaVersion string               `json:"schemaVersion"`
		Schema        map[string]string    `json:"schema"`
		Items         []descriptorResponse `json:"items"`
		Links         links                `json:"links"`
	}{version, map[string]string{"artifactType": artifactType, "artifactRevision": value.Value.Schema.Artifact.Revision}, items, links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/contexts", "schema": "/v1/releases/" + version + "/schemas/" + artifactType}})
}

func parsedDocument(value artifact.Descriptor) (json.RawMessage, error) {
	if !json.Valid(value.Artifact.Payload.Bytes) {
		return nil, postgres.ErrInconsistentState
	}
	return append(json.RawMessage(nil), value.Artifact.Payload.Bytes...), nil
}

func (s *Server) schemaPackage(c *gin.Context) {
	version, artifactType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, artifactType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	value, err := s.artifacts.SchemaPackage(c.Request.Context(), version, artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	schema, err := parsedDocument(value.Value.Schema)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	contexts := make([]any, 0, len(value.Value.Contexts))
	for _, item := range value.Value.Contexts {
		document, err := parsedDocument(item)
		if err != nil {
			s.writeArtifactError(c, err, SchemaNotInRelease)
			return
		}
		contexts = append(contexts, struct {
			Metadata descriptorResponse `json:"metadata"`
			Document json.RawMessage    `json:"document"`
		}{descriptor(item, version), document})
	}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, struct {
		SchemaVersion string `json:"schemaVersion"`
		Schema        any    `json:"schema"`
		Contexts      []any  `json:"contexts"`
		Links         links  `json:"links"`
	}{version, struct {
		Metadata descriptorResponse `json:"metadata"`
		Document json.RawMessage    `json:"document"`
	}{descriptor(value.Value.Schema, version), schema}, contexts, links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/package", "content": "/v1/releases/" + version + "/schemas/" + artifactType + "/content"}})
}

func enrichedMapping(value artifact.Mapping, version string) any {
	program := resourceResponse{Name: value.Detail.ProgramName, Role: "program", Digest: value.Detail.ProgramDigest, Href: "/v1/artifacts/" + value.Descriptor.Artifact.Revision + "/resources/" + value.Detail.ProgramName}
	for _, resource := range value.Descriptor.Resources {
		if resource.Name == value.Detail.ProgramName {
			program.MediaType, program.Digest, program.Size = resource.MediaType, resource.Digest, resource.Size
			break
		}
	}
	return struct {
		Metadata     descriptorResponse `json:"metadata"`
		SourceSchema any                `json:"sourceSchema"`
		TargetSchema any                `json:"targetSchema"`
		Cardinality  string             `json:"cardinality"`
		Engine       any                `json:"engine"`
		Program      resourceResponse   `json:"program"`
	}{descriptor(value.Descriptor, version), map[string]string{"artifactType": value.Detail.SourceArtifactType, "artifactRevision": value.Detail.SourceRevision, "metadata": "/v1/releases/" + version + "/schemas/" + value.Detail.SourceArtifactType, "content": "/v1/releases/" + version + "/schemas/" + value.Detail.SourceArtifactType + "/content"}, map[string]string{"artifactType": value.Detail.TargetArtifactType, "artifactRevision": value.Detail.TargetRevision, "schemaVersion": value.Detail.TargetSchemaVersion, "metadata": "/v1/releases/" + version + "/schemas/" + value.Detail.TargetArtifactType, "content": "/v1/releases/" + version + "/schemas/" + value.Detail.TargetArtifactType + "/content"}, value.Detail.Cardinality, map[string]string{"language": value.Detail.EngineLanguage, "languageVersion": value.Detail.EngineLanguageVersion, "executionProfile": value.Detail.ExecutionProfile}, program}
}

func (s *Server) mappings(c *gin.Context) {
	version := c.Param("schemaVersion")
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, "sourceArtifactType", "targetArtifactType", "engineLanguage", "executionProfile", "limit", "cursor")
	if !artifactmodel.ValidVersion(version) || err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	for _, key := range []string{"sourceArtifactType", "targetArtifactType", "engineLanguage", "executionProfile"} {
		if values.Get(key) == "" && c.Request.URL.Query().Has(key) {
			s.writeArtifactValidationError(c, InvalidQueryParameter)
			return
		}
	}
	for _, key := range []string{"sourceArtifactType", "targetArtifactType"} {
		if value := values.Get(key); value != "" {
			if !artifactmodel.ValidLogicalType(value) {
				s.writeArtifactValidationError(c, InvalidQueryParameter)
				return
			}
			if _, lookupErr := s.artifacts.GetReleaseArtifact(c.Request.Context(), version, "schema", value); lookupErr != nil {
				s.writeArtifactError(c, lookupErr, SchemaNotInRelease)
				return
			}
		}
	}
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 1000 {
			s.writeArtifactValidationError(c, InvalidQueryParameter)
			return
		}
	}
	filters := map[string]string{"version": version, "sourceArtifactType": values.Get("sourceArtifactType"), "targetArtifactType": values.Get("targetArtifactType"), "engineLanguage": values.Get("engineLanguage"), "executionProfile": values.Get("executionProfile")}
	var afterType *string
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: "release-source-mappings", Limit: limit, Sort: "artifact-type", Filters: filters})
		if decodeErr != nil || !artifactmodel.ValidLogicalType(payload.Last) {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		afterType = &payload.Last
	}
	result, err := s.artifacts.Mappings(c.Request.Context(), version, model.ArtifactFilter{Kind: "source-mapping"}, afterType, limit+1)
	if err != nil {
		s.writeArtifactError(c, err, ReleaseNotFound)
		return
	}
	items := make([]any, 0)
	matchedTypes := make([]string, 0)
	seenSources := make(map[string]struct{})
	for _, item := range result.Value {
		d := item.Detail
		if _, exists := seenSources[d.SourceArtifactType]; exists {
			s.writeArtifactError(c, postgres.ErrInconsistentState, ReleaseNotFound)
			return
		}
		seenSources[d.SourceArtifactType] = struct{}{}
		if values.Get("sourceArtifactType") != "" && d.SourceArtifactType != values.Get("sourceArtifactType") || values.Get("targetArtifactType") != "" && d.TargetArtifactType != values.Get("targetArtifactType") || values.Get("engineLanguage") != "" && d.EngineLanguage != values.Get("engineLanguage") || values.Get("executionProfile") != "" && d.ExecutionProfile != values.Get("executionProfile") {
			continue
		}
		items = append(items, enrichedMapping(item, version))
		matchedTypes = append(matchedTypes, item.Descriptor.Artifact.Type)
	}
	next := (*string)(nil)
	if len(items) > limit {
		last := matchedTypes[limit-1]
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: "release-source-mappings", Limit: limit, Sort: "artifact-type", Filters: filters, Last: last, Snapshot: version})
		if encodeErr != nil {
			s.writeArtifactError(c, postgres.ErrInconsistentState, ReleaseNotFound)
			return
		}
		next, items = &token, items[:limit]
	}
	s.writeJSON(c, http.StatusOK, result.Current.SchemaVersion, version, struct {
		SchemaVersion string  `json:"schemaVersion"`
		Items         []any   `json:"items"`
		NextCursor    *string `json:"nextCursor"`
		Links         links   `json:"links"`
	}{version, items, next, links{"self": "/v1/releases/" + version + "/source-mappings"}})
}

func (s *Server) schemaMapping(c *gin.Context) {
	version, source := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, source) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if _, err := s.artifacts.GetReleaseArtifact(c.Request.Context(), version, "schema", source); err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	value, err := s.artifacts.MappingPackage(c.Request.Context(), version, source)
	if err != nil {
		s.writeArtifactError(c, err, SourceMappingNotInRelease)
		return
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, enrichedMapping(artifact.Mapping{Descriptor: value.Value.Mapping, Detail: value.Value.Detail}, version))
}

func (s *Server) mappingPackage(c *gin.Context) {
	version, source := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, source) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if _, err := s.artifacts.GetReleaseArtifact(c.Request.Context(), version, "schema", source); err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	value, err := s.artifacts.MappingPackage(c.Request.Context(), version, source)
	if err != nil {
		s.writeArtifactError(c, err, SourceMappingNotInRelease)
		return
	}
	sourceDocument, sourceErr := parsedDocument(value.Value.Source)
	targetDocument, targetErr := parsedDocument(value.Value.Target)
	if sourceErr != nil || targetErr != nil {
		s.writeArtifactError(c, postgres.ErrInconsistentState, SourceMappingNotInRelease)
		return
	}
	stores := make([]string, 0, len(value.Value.Stores))
	for _, entry := range value.Value.Stores {
		stores = append(stores, entry.Store)
	}
	body := struct {
		SchemaVersion string   `json:"schemaVersion"`
		Mapping       any      `json:"mapping"`
		Program       any      `json:"program"`
		SourceSchema  any      `json:"sourceSchema"`
		TargetSchema  any      `json:"targetSchema"`
		TargetStores  []string `json:"targetStores"`
		Links         links    `json:"links"`
	}{version, enrichedMapping(artifact.Mapping{Descriptor: value.Value.Mapping, Detail: value.Value.Detail}, version), struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
		Content   string `json:"content"`
	}{value.Value.Program.Name, value.Value.Program.Role, value.Value.Program.MediaType, value.Value.Program.Digest, value.Value.Program.Size, base64.StdEncoding.EncodeToString(value.Value.Program.Bytes)}, struct {
		Metadata descriptorResponse `json:"metadata"`
		Document json.RawMessage    `json:"document"`
	}{descriptor(value.Value.Source, version), sourceDocument}, struct {
		Metadata descriptorResponse `json:"metadata"`
		Document json.RawMessage    `json:"document"`
	}{descriptor(value.Value.Target, version), targetDocument}, stores, links{"self": "/v1/releases/" + version + "/schemas/" + source + "/source-mapping/package"}}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, body)
}

func (s *Server) schemaStores(c *gin.Context) {
	version, artifactType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, artifactType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	metadata, err := s.artifacts.ReleaseDescriptor(c.Request.Context(), version, "schema", artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	value, err := s.artifacts.SchemaStores(c.Request.Context(), version, artifactType)
	if err != nil {
		s.writeArtifactError(c, err, SchemaNotInRelease)
		return
	}
	stores := make([]string, 0, len(value.Value))
	for _, entry := range value.Value {
		stores = append(stores, entry.Store)
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, struct {
		SchemaVersion string   `json:"schemaVersion"`
		Schema        any      `json:"schema"`
		Stores        []string `json:"stores"`
		Links         links    `json:"links"`
	}{version, map[string]string{"artifactType": artifactType, "artifactRevision": metadata.Value.Artifact.Revision, "schemaClass": *metadata.Value.Artifact.SchemaClass}, stores, links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/stores", "schema": "/v1/releases/" + version + "/schemas/" + artifactType}})
}

func (s *Server) writeBoundedJSON(c *gin.Context, schema, selected string, value any) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > maxGeneratedJSONBytes {
		s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
		return
	}
	if _, ok := NegotiateMediaType(c.GetHeader("Accept"), "application/json"); !ok {
		s.writeReleaseErrorResponse(c, NotAcceptable, 0, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
		return
	}
	if err := WriteRepresentation(c.Writer, Representation{Status: http.StatusOK, MediaType: "application/json", Body: body, Schema: schema, Selected: selected, Method: c.Request.Method, AcceptCoding: c.GetHeader("Accept-Encoding"), Context: c.Request.Context(), IfNoneMatch: c.GetHeader("If-None-Match")}); err != nil && !c.Writer.Written() {
		s.writeReleaseRepresentationError(c, ErrorMetadata{SchemaVersion: schema, SelectedSchemaVersion: selected})
	}
}
