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

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/representation"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

// SchemasResponse represents a paginated schema descriptor response.
type SchemasResponse struct {
	SchemaVersion string               `json:"schemaVersion"`
	Items         []descriptorResponse `json:"items"`
	NextCursor    *string              `json:"nextCursor"`
	Links         links                `json:"links"`
}

// PackageSchemaResponse represents a schema document in a source-mapping or schema package response.
type PackageSchemaResponse = representation.PackageSchemaResponse

// SchemaContextsResponse represents the context descriptors for a schema.
type SchemaContextsResponse struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Schema        SchemaReferenceResponse `json:"schema"`
	Items         []descriptorResponse    `json:"items"`
	Links         links                   `json:"links"`
}

// SchemaReferenceResponse represents a schema artifact reference.
type SchemaReferenceResponse struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
}

// SchemaPackageResponse represents a schema document and its context documents.
type SchemaPackageResponse struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Schema        PackageSchemaResponse   `json:"schema"`
	Contexts      []PackageSchemaResponse `json:"contexts"`
	Links         links                   `json:"links"`
}

// MappingResponse represents a source-mapping descriptor response.
type MappingResponse = representation.MappingResponse

type MappingProgramsResponse = representation.MappingProgramsResponse

type MappingTransformationResponse = representation.MappingTransformationResponse

// MappingSourceSchemaResponse represents the source schema for a source mapping.
type MappingSourceSchemaResponse = representation.MappingSourceSchemaResponse

// MappingEngineResponse represents the engine configuration for a source mapping.
type MappingEngineResponse = representation.MappingEngineResponse

// MappingsResponse represents a paginated source-mapping descriptor response.
type MappingsResponse struct {
	SchemaVersion string            `json:"schemaVersion"`
	Items         []MappingResponse `json:"items"`
	NextCursor    *string           `json:"nextCursor"`
	Links         links             `json:"links"`
}

// MappingPackageResponse represents a source-mapping package response.
type MappingPackageResponse = representation.MappingPackageResponse

type MappingPackagesResponse struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Items         []MappingPackageResponse `json:"items"`
	NextCursor    *string                  `json:"nextCursor"`
	Links         links                    `json:"links"`
}

// MappingPackageProgramResponse represents program content in a source-mapping package response.
type MappingPackageProgramResponse = representation.MappingPackageProgramResponse

// SchemaStoresResponse represents the stores associated with a schema.
type SchemaStoresResponse struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Schema        SchemaStoreSchemaResponse `json:"schema"`
	Stores        []string                  `json:"stores"`
	Links         links                     `json:"links"`
}

// SchemaStoreSchemaResponse represents a schema reference in a stores response.
type SchemaStoreSchemaResponse struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
	SchemaClass      string `json:"schemaClass"`
}

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
	items := make([]descriptorResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, descriptor(item, version))
	}
	s.writeJSON(c, http.StatusOK, page.Current.SchemaVersion, version, SchemasResponse{
		SchemaVersion: version,
		Items:         items,
		NextCursor:    next,
		Links:         links{"self": "/v1/releases/" + version + "/schemas"},
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
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, SchemaContextsResponse{
		SchemaVersion: version,
		Schema: SchemaReferenceResponse{
			ArtifactType:     artifactType,
			ArtifactRevision: value.Value.Schema.Artifact.Revision,
		},
		Items: items,
		Links: links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/contexts", "schema": "/v1/releases/" + version + "/schemas/" + artifactType},
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
	contexts := make([]PackageSchemaResponse, 0, len(value.Value.Contexts))
	for _, item := range value.Value.Contexts {
		document, err := parsedDocument(item)
		if err != nil {
			s.writeArtifactError(c, err, SchemaNotInRelease)
			return
		}
		contexts = append(contexts, PackageSchemaResponse{
			Metadata: descriptor(item, version),
			Document: document,
		})
	}
	s.writeBoundedJSON(c, value.Current.SchemaVersion, version, SchemaPackageResponse{
		SchemaVersion: version,
		Schema: PackageSchemaResponse{
			Metadata: descriptor(value.Value.Schema, version),
			Document: schema,
		},
		Contexts: contexts,
		Links:    links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/package", "content": "/v1/releases/" + version + "/schemas/" + artifactType + "/content"},
	})
}

func enrichedMapping(value artifact.Mapping, version string) MappingResponse {
	return representation.BuildMapping(value, version)
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
		if values.Get(key) == "" || len(values.Get(key)) > config.MaxIdentifierBytes && key != "cursor" {
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
	items := make([]MappingResponse, 0)
	packageItems := make([]MappingPackageResponse, 0)
	matchedTypes := make([]string, 0)
	encodedItems := 0
	withinItemBudget := func(value any) bool {
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		encodedItems += len(encoded)
		return int64(encodedItems) <= config.MaxGeneratedJSONBytes
	}
	if packages {
		result, lookupErr := s.artifacts.MappingPackages(c.Request.Context(), version, filter, afterType, limit+1)
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
		result, err := s.artifacts.Mappings(c.Request.Context(), version, filter, afterType, limit+1)
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
		s.writeBoundedJSON(c, current, version, MappingPackagesResponse{SchemaVersion: version, Items: packageItems, NextCursor: next, Links: links{"self": self}})
		return
	}
	s.writeBoundedJSON(c, current, version, MappingsResponse{
		SchemaVersion: version,
		Items:         items,
		NextCursor:    next,
		Links:         links{"self": self},
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
	value, err := s.artifacts.MappingMetadata(c.Request.Context(), version, mappingType)
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
	value, err := s.artifacts.MappingPackage(c.Request.Context(), version, mappingType)
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

func mappingPackageBody(value artifact.MappingPackage, version string) (MappingPackageResponse, error) {
	return representation.BuildMappingPackage(value, version)
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
	s.writeJSON(c, http.StatusOK, value.Current.SchemaVersion, version, SchemaStoresResponse{
		SchemaVersion: version,
		Schema: SchemaStoreSchemaResponse{
			ArtifactType:     artifactType,
			ArtifactRevision: metadata.Value.Artifact.Revision,
			SchemaClass:      *metadata.Value.Artifact.SchemaClass,
		},
		Stores: stores,
		Links:  links{"self": "/v1/releases/" + version + "/schemas/" + artifactType + "/stores", "schema": "/v1/releases/" + version + "/schemas/" + artifactType},
	})
}

func (s *Server) writeBoundedJSON(c *gin.Context, schema, selected string, value any) {
	body, err := json.Marshal(value)
	if err != nil || int64(len(body)) > config.MaxGeneratedJSONBytes {
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
