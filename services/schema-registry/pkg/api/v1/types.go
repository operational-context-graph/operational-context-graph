// Package v1 defines the version 1 artifact and package JSON contract.
package v1

import (
	"encoding/json"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	manifestmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	sourcemappingmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

// LinkType identifies a navigation relation in an API representation.
type LinkType string

const (
	LinkTypeArtifact       LinkType = "artifact"
	LinkTypeArtifacts      LinkType = "artifacts"
	LinkTypeContent        LinkType = "content"
	LinkTypeContexts       LinkType = "contexts"
	LinkTypeDescriptor     LinkType = "descriptor"
	LinkTypeFrom           LinkType = "from"
	LinkTypeHistory        LinkType = "history"
	LinkTypeManifest       LinkType = "manifest"
	LinkTypeMapping        LinkType = "mapping"
	LinkTypeMetadata       LinkType = "metadata"
	LinkTypePackage        LinkType = "package"
	LinkTypeRelease        LinkType = "release"
	LinkTypeReleases       LinkType = "releases"
	LinkTypeSchema         LinkType = "schema"
	LinkTypeSchemas        LinkType = "schemas"
	LinkTypeSelf           LinkType = "self"
	LinkTypeSourceMappings LinkType = "sourceMappings"
	LinkTypeStores         LinkType = "stores"
	LinkTypeTo             LinkType = "to"
)

// Links contains navigation links indexed by their relation type.
type Links = map[LinkType]string

// Page contains a collection of API resources and its pagination metadata.
type Page[T any] struct {
	SchemaVersion string  `json:"schemaVersion"`
	Items         []T     `json:"items"`
	NextCursor    *string `json:"nextCursor"`
	Links         Links   `json:"links"`
}

// Registry identifies the active release and its discovery links.
type Registry struct {
	SchemaVersion  string `json:"schemaVersion"`
	ManifestDigest string `json:"manifestDigest"`
	Links          Links  `json:"links"`
}

// Release describes an activated schema release.
type Release struct {
	SchemaVersion   string                      `json:"schemaVersion"`
	ManifestDigest  string                      `json:"manifestDigest"`
	PreviousRelease *ReleaseReference           `json:"previousRelease"`
	Source          ReleaseSource               `json:"source"`
	Compatibility   manifestmodel.Compatibility `json:"compatibility,omitempty"`
	Changes         json.RawMessage             `json:"changes,omitempty"`
	Links           Links                       `json:"links"`
}

// ReleaseReference identifies a release and its immutable manifest.
type ReleaseReference struct {
	SchemaVersion  string `json:"schemaVersion"`
	ManifestDigest string `json:"manifestDigest"`
}

// ReleaseSource identifies the source control revision for a release.
type ReleaseSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

// Changes describes the changes between two schema releases.
type Changes struct {
	FromSchemaVersion     string                       `json:"fromSchemaVersion"`
	ToSchemaVersion       string                       `json:"toSchemaVersion"`
	Compatibility         manifestmodel.Compatibility  `json:"compatibility"`
	Artifacts             []ArtifactChange             `json:"artifacts"`
	SchemaContextBindings []SchemaContextBindingChange `json:"schemaContextBindings"`
	URICatalog            []URIChange                  `json:"uriCatalog"`
	StorageCatalog        []StorageChange              `json:"storageCatalog"`
	Links                 Links                        `json:"links"`
}

// ArtifactChange describes one artifact change between releases.
type ArtifactChange struct {
	Operation      manifestmodel.Operation `json:"operation"`
	Kind           artifactmodel.Kind      `json:"kind"`
	ArtifactType   string                  `json:"artifactType"`
	BeforeRevision string                  `json:"beforeRevision,omitempty"`
	AfterRevision  string                  `json:"afterRevision,omitempty"`
	Impact         manifestmodel.Impact    `json:"impact"`
}

// SchemaContextBindingChange describes one schema context binding change.
type SchemaContextBindingChange struct {
	Operation       manifestmodel.Operation `json:"operation"`
	SchemaRevision  string                  `json:"schemaRevision"`
	ContextRevision string                  `json:"contextRevision"`
	Impact          manifestmodel.Impact    `json:"impact"`
}

// URIChange describes one Uniform Resource Identifier catalog change.
type URIChange struct {
	Operation      manifestmodel.Operation `json:"operation"`
	URI            string                  `json:"uri"`
	BeforeRevision string                  `json:"beforeRevision,omitempty"`
	BeforeResource string                  `json:"beforeResource,omitempty"`
	AfterRevision  string                  `json:"afterRevision,omitempty"`
	AfterResource  string                  `json:"afterResource,omitempty"`
	Impact         manifestmodel.Impact    `json:"impact"`
}

// StorageChange describes one storage catalog change.
type StorageChange struct {
	Operation    manifestmodel.Operation `json:"operation"`
	Kind         artifactmodel.Kind      `json:"kind"`
	ArtifactType string                  `json:"artifactType"`
	Store        manifestmodel.Store     `json:"store"`
	Impact       manifestmodel.Impact    `json:"impact"`
}

// ArtifactDescriptor describes an artifact without embedding its stored bytes.
type ArtifactDescriptor struct {
	SchemaVersion             string                     `json:"schemaVersion,omitempty"`
	Kind                      artifactmodel.Kind         `json:"kind"`
	ArtifactType              string                     `json:"artifactType"`
	ArtifactRevision          string                     `json:"artifactRevision"`
	CreatedUnderSchemaVersion string                     `json:"createdUnderSchemaVersion"`
	SchemaClass               *artifactmodel.SchemaClass `json:"schemaClass,omitempty"`
	TargetSchemaVersion       *string                    `json:"targetSchemaVersion,omitempty"`
	CanonicalURI              string                     `json:"canonicalUri"`
	Payload                   Payload                    `json:"payload"`
	Resources                 []Resource                 `json:"resources"`
	Dependencies              []Dependency               `json:"dependencies"`
	Extensions                json.RawMessage            `json:"extensions,omitempty"`
	Links                     Links                      `json:"links"`
}

// ArtifactHistoryItem identifies an artifact revision selected by a release.
type ArtifactHistoryItem struct {
	SchemaVersion             string `json:"schemaVersion"`
	ArtifactRevision          string `json:"artifactRevision"`
	CreatedUnderSchemaVersion string `json:"createdUnderSchemaVersion"`
	Links                     Links  `json:"links"`
}

// ArtifactBytes contains an artifact representation and its response metadata.
type ArtifactBytes struct {
	MediaType    string
	Digest       string
	Bytes        []byte
	Revision     string
	ResourceName string
}

// Payload identifies exact payload bytes and their immutable retrieval link.
type Payload struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Href      string `json:"href"`
}

// Resource describes one immutable named resource.
type Resource struct {
	Name      string                     `json:"name"`
	Role      artifactmodel.ResourceRole `json:"role"`
	MediaType string                     `json:"mediaType"`
	Digest    string                     `json:"digest"`
	Size      int64                      `json:"size"`
	Href      string                     `json:"href"`
}

// Dependency identifies an exact dependency and its metadata link.
type Dependency struct {
	Kind             artifactmodel.Kind `json:"kind"`
	ArtifactType     string             `json:"artifactType"`
	ArtifactRevision string             `json:"artifactRevision"`
	Href             string             `json:"href"`
}

// SourceMapping is the complete, enriched source-mapping JSON model.
type SourceMapping struct {
	ArtifactDescriptor
	SourceSchema SourceMappingSourceSchema `json:"sourceSchema"`
	Engine       SourceMappingEngine       `json:"engine"`
	Programs     SourceMappingPrograms     `json:"programs"`
}

// SourceMappingPrograms preserves an optional condition and ordered transformations.
type SourceMappingPrograms struct {
	Condition       *Resource                     `json:"condition,omitempty"`
	Transformations []SourceMappingTransformation `json:"transformations"`
}

// SourceMappingTransformation describes one program binding and its canonical output.
type SourceMappingTransformation struct {
	Resource
	TargetSchema SourceMappingSourceSchema      `json:"targetSchema"`
	Cardinality  sourcemappingmodel.Cardinality `json:"cardinality"`
}

// SourceMappingSourceSchema resolves a schema reference within the selected release.
type SourceMappingSourceSchema struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
	Content          string `json:"content"`
}

// SourceMappingEngine carries opaque, publisher-declared engine metadata.
type SourceMappingEngine struct {
	ExecutionProfile string `json:"executionProfile"`
	Language         string `json:"language"`
	LanguageVersion  string `json:"languageVersion"`
}

// SchemaDocument embeds a JSON schema's complete metadata and parsed JSON value.
type SchemaDocument struct {
	Metadata ArtifactDescriptor `json:"metadata"`
	Document json.RawMessage    `json:"document"`
}

// SourceMappingPackage is shared by direct package bodies and collection items.
type SourceMappingPackage struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Mapping       SourceMapping                 `json:"mapping"`
	Programs      []SourceMappingProgramContent `json:"programs"`
	Schemas       []SchemaDocument              `json:"schemas"`
	TargetStores  []manifestmodel.Store         `json:"targetStores"`
	Links         Links                         `json:"links"`
}

// SourceMappingProgramContent embeds canonical Base64 content for one referenced program.
type SourceMappingProgramContent struct {
	Name      string                     `json:"name"`
	Role      artifactmodel.ResourceRole `json:"role"`
	MediaType string                     `json:"mediaType"`
	Digest    string                     `json:"digest"`
	Size      int64                      `json:"size"`
	Content   string                     `json:"content"`
}

// SchemaReference identifies a schema artifact revision.
type SchemaReference struct {
	ArtifactRevision string `json:"artifactRevision"`
	ArtifactType     string `json:"artifactType"`
}

// SchemaContexts contains the JSON-LD context descriptors for a schema.
type SchemaContexts struct {
	SchemaVersion string               `json:"schemaVersion"`
	Schema        SchemaReference      `json:"schema"`
	Items         []ArtifactDescriptor `json:"items"`
	Links         Links                `json:"links"`
}

// SchemaPackage contains a schema document and its context documents.
type SchemaPackage struct {
	SchemaVersion string           `json:"schemaVersion"`
	Schema        SchemaDocument   `json:"schema"`
	Contexts      []SchemaDocument `json:"contexts"`
	Links         Links            `json:"links"`
}

// SchemaStores identifies the stores associated with a schema.
type SchemaStores struct {
	SchemaVersion string                `json:"schemaVersion"`
	Schema        SchemaStoreSchema     `json:"schema"`
	Stores        []manifestmodel.Store `json:"stores"`
	Links         Links                 `json:"links"`
}

// SchemaStoreSchema identifies a schema in a stores response.
type SchemaStoreSchema struct {
	ArtifactRevision string                    `json:"artifactRevision"`
	ArtifactType     string                    `json:"artifactType"`
	SchemaClass      artifactmodel.SchemaClass `json:"schemaClass"`
}
