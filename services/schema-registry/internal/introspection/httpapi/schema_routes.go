// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/limits"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	manifestmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

func (s *Server) registerSchemaRoutes() {
	if s.artifacts == nil {
		return
	}
	for _, route := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{"/releases/:schemaVersion/schemas/:artifactType/source-mappings/packages", s.schemaMappingPackages},
		{"/releases/:schemaVersion/schemas/:artifactType/source-mappings", s.schemaMapping},
		{"/releases/:schemaVersion/schemas/:artifactType/contexts", s.schemaContexts},
		{"/releases/:schemaVersion/schemas/:artifactType/package", s.schemaPackage},
		{"/releases/:schemaVersion/schemas/:artifactType/content", s.schemaContent},
		{"/releases/:schemaVersion/schemas/:artifactType/stores", s.schemaStores},
		{"/releases/:schemaVersion/schemas/:artifactType", s.schemaMetadata},
		{"/releases/:schemaVersion/schemas", s.schemas},
		{"/releases/:schemaVersion/source-mappings", s.mappings},
		{"/releases/:schemaVersion/source-mappings/:artifactType", s.mappingMetadata},
		{"/releases/:schemaVersion/source-mappings/:artifactType/package", s.mappingPackage},
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
	items := make([]v1.ArtifactDescriptor, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, descriptor(item, version))
	}
	s.writeJSON(c, http.StatusOK, page.Current.SchemaVersion, version, v1.Page[v1.ArtifactDescriptor]{
		SchemaVersion: version,
		Items:         items,
		NextCursor:    next,
		Links:         v1.Links{v1.LinkTypeSelf: "/v1/releases/" + version + "/schemas"},
	})
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
	s.writeArtifactBytes(c, value.Current.SchemaVersion, version, v1.ArtifactBytes{
		MediaType: value.Value.Payload.MediaType,
		Digest:    value.Value.Payload.Digest,
		Bytes:     value.Value.Payload.Bytes,
		Revision:  value.Value.Revision,
	})
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
	items := make([]v1.ArtifactDescriptor, 0, len(value.Value.Contexts))
	for _, item := range value.Value.Contexts {
		items = append(items, descriptor(item, version))
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, v1.SchemaContexts{
		SchemaVersion: version,
		Schema: v1.SchemaReference{
			ArtifactType:     artifactType,
			ArtifactRevision: value.Value.Schema.Artifact.Revision,
		},
		Items: items,
		Links: v1.Links{
			v1.LinkTypeSelf:   "/v1/releases/" + version + "/schemas/" + artifactType + "/contexts",
			v1.LinkTypeSchema: "/v1/releases/" + version + "/schemas/" + artifactType,
		},
	})
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
	contexts := make([]v1.SchemaDocument, 0, len(value.Value.Contexts))
	for _, item := range value.Value.Contexts {
		document, err := parsedDocument(item)
		if err != nil {
			s.writeArtifactError(c, err, SchemaNotInRelease)
			return
		}
		contexts = append(contexts, v1.SchemaDocument{
			Metadata: descriptor(item, version),
			Document: document,
		})
	}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, v1.SchemaPackage{
		SchemaVersion: version,
		Schema: v1.SchemaDocument{
			Metadata: descriptor(value.Value.Schema, version),
			Document: schema,
		},
		Contexts: contexts,
		Links: v1.Links{
			v1.LinkTypeSelf:    "/v1/releases/" + version + "/schemas/" + artifactType + "/package",
			v1.LinkTypeContent: "/v1/releases/" + version + "/schemas/" + artifactType + "/content",
		},
	})
}

func enrichedMapping(value model.SourceMapping, version string) v1.SourceMapping {
	return BuildMapping(value, version)
}

func (s *Server) mappings(c *gin.Context)              { s.mappingCollection(c, false, false) }
func (s *Server) schemaMapping(c *gin.Context)         { s.mappingCollection(c, true, false) }
func (s *Server) schemaMappingPackages(c *gin.Context) { s.mappingCollection(c, true, true) }

func (s *Server) mappingCollection(c *gin.Context, nested, packages bool) {
	version := c.Param("schemaVersion")
	allowed := []string{"sourceArtifactType", "targetArtifactType", "engineLanguage", "executionProfile", "limit", "cursor"}
	if nested {
		allowed = []string{"sourceMappingArtifactType", "limit", "cursor"}
	}
	values, err := ParseAndValidateQuery(c.Request.URL.RawQuery, allowed...)
	if !artifactmodel.ValidVersion(version) || nested && !artifactmodel.ValidLogicalType(c.Param("artifactType")) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	for key := range values {
		if values.Get(key) == "" || len(values.Get(key)) > schemaartifacts.MaxIdentifierBytes && key != "cursor" {
			s.writeArtifactValidationError(c, InvalidQueryParameter)
			return
		}
	}
	for _, key := range []string{"sourceArtifactType", "targetArtifactType", "sourceMappingArtifactType"} {
		if value := values.Get(key); value != "" {
			if !artifactmodel.ValidLogicalType(value) {
				s.writeArtifactValidationError(c, InvalidQueryParameter)
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
	filter := model.ArtifactFilter{Kind: "source-mapping", ArtifactType: values.Get("sourceMappingArtifactType"), SourceArtifactType: values.Get("sourceArtifactType"), TargetArtifactType: values.Get("targetArtifactType"), EngineLanguage: values.Get("engineLanguage"), ExecutionProfile: values.Get("executionProfile")}
	resource := "release-source-mappings"
	if nested {
		filter.SourceArtifactType = c.Param("artifactType")
		resource = "source-schema-mappings"
	}
	if packages {
		resource = "source-schema-mapping-packages"
	}
	filters := map[string]string{"version": version, "sourceArtifactType": filter.SourceArtifactType, "targetArtifactType": filter.TargetArtifactType, "engineLanguage": filter.EngineLanguage, "executionProfile": filter.ExecutionProfile, "sourceMappingArtifactType": filter.ArtifactType}
	var afterType *string
	if token := values.Get("cursor"); token != "" {
		payload, decodeErr := s.cursors.Decode(token, pagination.Payload{Resource: resource, Limit: limit, Sort: "artifact-type", Filters: filters})
		if decodeErr != nil || !artifactmodel.ValidLogicalType(payload.Last) || payload.Snapshot != version {
			s.writeArtifactValidationError(c, InvalidCursor)
			return
		}
		afterType = &payload.Last
	}
	self := "/v1/releases/" + version + "/source-mappings"
	if nested {
		self = "/v1/releases/" + version + "/schemas/" + c.Param("artifactType") + "/source-mappings"
	}
	if packages {
		self += "/packages"
	}
	if query := values.Encode(); query != "" {
		self += "?" + query
	}
	var current string
	items := make([]v1.SourceMapping, 0)
	packageItems := make([]v1.SourceMappingPackage, 0)
	matchedTypes := make([]string, 0)
	encodedItems := 0
	withinItemBudget := func(value any) bool {
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		encodedItems += len(encoded)
		return int64(encodedItems) <= limits.MaxGeneratedJSONBytes
	}
	if packages {
		result, lookupErr := s.artifacts.SourceMappingPackages(c.Request.Context(), version, filter, afterType, limit+1)
		if lookupErr != nil {
			s.writeArtifactError(c, lookupErr, SchemaNotInRelease)
			return
		}
		current = result.Current.SchemaVersion
		for index, item := range result.Value {
			matchedTypes = append(matchedTypes, item.Mapping.Artifact.Type)
			if index >= limit {
				continue
			}
			body, err := mappingPackageBody(item, version)
			if err != nil {
				s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: current, SelectedSchemaVersion: version})
				return
			}
			if !withinItemBudget(body) {
				s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: current, SelectedSchemaVersion: version})
				return
			}
			packageItems = append(packageItems, body)
		}
	} else {
		result, err := s.artifacts.SourceMappings(c.Request.Context(), version, filter, afterType, limit+1)
		if err != nil {
			s.writeArtifactError(c, err, SchemaNotInRelease)
			return
		}
		current = result.Current.SchemaVersion
		for index, item := range result.Value {
			matchedTypes = append(matchedTypes, item.Descriptor.Artifact.Type)
			if index >= limit {
				continue
			}
			body := enrichedMapping(item, version)
			if !withinItemBudget(body) {
				s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: current, SelectedSchemaVersion: version})
				return
			}
			items = append(items, body)
		}
	}
	next := (*string)(nil)
	if len(matchedTypes) > limit {
		last := matchedTypes[limit-1]
		token, encodeErr := s.cursors.Encode(pagination.Payload{Resource: resource, Limit: limit, Sort: "artifact-type", Filters: filters, Last: last, Snapshot: version})
		if encodeErr != nil {
			s.writeArtifactError(c, postgres.ErrInconsistentState, ReleaseNotFound)
			return
		}
		next = &token
		if !packages {
			items = items[:limit]
		}
	}
	if packages {
		s.writeBoundedJSON(c, current, version, v1.Page[v1.SourceMappingPackage]{
			SchemaVersion: version,
			Items:         packageItems,
			NextCursor:    next,
			Links:         v1.Links{v1.LinkTypeSelf: self},
		})
		return
	}
	s.writeBoundedJSON(c, current, version, v1.Page[v1.SourceMapping]{
		SchemaVersion: version,
		Items:         items,
		NextCursor:    next,
		Links:         v1.Links{v1.LinkTypeSelf: self},
	})
}

func (s *Server) mappingMetadata(c *gin.Context) {
	version, mappingType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, mappingType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if _, err := ParseAndValidateQuery(c.Request.URL.RawQuery); err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	value, err := s.artifacts.SourceMappingMetadata(c.Request.Context(), version, mappingType)
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotInRelease)
		return
	}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, enrichedMapping(value.Value, version))
}

func (s *Server) mappingPackage(c *gin.Context) {
	version, mappingType := c.Param("schemaVersion"), c.Param("artifactType")
	if !validSchemaPath(version, mappingType) {
		s.writeArtifactValidationError(c, InvalidRequest)
		return
	}
	if _, err := ParseAndValidateQuery(c.Request.URL.RawQuery); err != nil {
		s.writeArtifactValidationError(c, InvalidQueryParameter)
		return
	}
	value, err := s.artifacts.SourceMappingPackage(c.Request.Context(), version, mappingType)
	if err != nil {
		s.writeArtifactError(c, err, ArtifactNotInRelease)
		return
	}
	body, err := mappingPackageBody(value.Value, version)
	if err != nil {
		s.writeReleaseErrorResponse(c, InconsistentRegistryState, 0, ErrorMetadata{SchemaVersion: value.Current.SchemaVersion, SelectedSchemaVersion: version})
		return
	}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, body)
}

func mappingPackageBody(value model.SourceMappingPackage, version string) (v1.SourceMappingPackage, error) {
	return BuildMappingPackage(value, version)
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
	stores := make([]manifestmodel.Store, 0, len(value.Value))
	for _, entry := range value.Value {
		stores = append(stores, manifestmodel.Store(entry.Store))
	}
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, v1.SchemaStores{
		SchemaVersion: version,
		Schema: v1.SchemaStoreSchema{
			ArtifactType:     artifactType,
			ArtifactRevision: metadata.Value.Artifact.Revision,
			SchemaClass:      artifactmodel.SchemaClass(*metadata.Value.Artifact.SchemaClass),
		},
		Stores: stores,
		Links: v1.Links{
			v1.LinkTypeSelf:   "/v1/releases/" + version + "/schemas/" + artifactType + "/stores",
			v1.LinkTypeSchema: "/v1/releases/" + version + "/schemas/" + artifactType,
		},
	})
}

func (s *Server) writeBoundedJSON(c *gin.Context, schema, selected string, value any) {
	body, err := json.Marshal(value)
	if err != nil || int64(len(body)) > limits.MaxGeneratedJSONBytes {
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
