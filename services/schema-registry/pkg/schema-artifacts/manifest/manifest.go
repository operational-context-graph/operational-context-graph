// Package manifest parses and validates immutable Schema Release Manifests.
package manifest

import (
	"errors"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// Version identifies a supported release manifest format version.
type Version string

const (
	// MediaType identifies the version 1 Schema Release Manifest representation.
	MediaType = "application/vnd.apeiro.ocg.schema-release-manifest.v1+json"
	// FormatVersion is the supported manifest format version, independent of the selected schema release.
	FormatVersion Version = "1.0.0"
)

var (
	// ErrInvalid classifies malformed or semantically invalid manifest input.
	ErrInvalid = errors.New("invalid release manifest")
	// errInvalid preserves the package-local spelling of the public validation sentinel.
	errInvalid = ErrInvalid
)

// Compatibility classifies the overall effect of a release.
type Compatibility string

const (
	// CompatibilityInitial identifies the first release, whose machine-readable change collections are empty.
	CompatibilityInitial Compatibility = "initial"
	// CompatibilityBreaking requires the next major schema version and resets minor and patch components.
	CompatibilityBreaking Compatibility = "breaking"
	// CompatibilityAdditive requires the next minor schema version and resets the patch component.
	CompatibilityAdditive Compatibility = "additive"
	// CompatibilityOperational requires the next patch schema version without changing major or minor components.
	CompatibilityOperational Compatibility = "operational"
)

// Operation identifies a change to an artifact or catalog entry.
// Binding and storage changes support only add and remove.
type Operation string

const (
	// OperationAdd introduces an artifact selection or catalog association.
	OperationAdd Operation = "add"
	// OperationUpdate replaces an artifact revision or URI catalog target.
	OperationUpdate Operation = "update"
	// OperationRemove removes an artifact selection or catalog association.
	OperationRemove Operation = "remove"
)

// Impact classifies one change. Unlike Compatibility, it excludes initial.
type Impact string

const (
	// ImpactBreaking marks a change that alters existing interpretation or has unproven compatibility.
	ImpactBreaking Impact = "breaking"
	// ImpactAdditive marks a new capability that preserves existing interpretation and destinations.
	ImpactAdditive Impact = "additive"
	// ImpactOperational marks a change that preserves the format-defined interpretation of existing data.
	ImpactOperational Impact = "operational"
)

// Store identifies a registered logical Operational Context Graph store.
type Store string

const (
	// StoreDataCenterInventory identifies the logical inventory store, not a physical database.
	StoreDataCenterInventory Store = "data-center-inventory"
	// StoreTelemetryStore identifies the logical telemetry destination.
	StoreTelemetryStore Store = "telemetry-store"
	// StoreInsightsRepository identifies the logical destination for derived insights.
	StoreInsightsRepository Store = "insights-repository"
	// StoreRecordedActionsRepository identifies the logical destination for recorded operational actions.
	StoreRecordedActionsRepository Store = "recorded-actions-repository"
)

// Manifest is the complete selected artifact closure for one schema version.
// Raw contains the submitted authoritative JCS bytes after Parse succeeds.
type Manifest struct {
	// Schema identifies the manifest format schema through an absolute URI.
	Schema string
	// FormatVersion selects the manifest format independently of SchemaVersion.
	FormatVersion Version
	// SchemaVersion identifies this immutable release of the complete artifact selection.
	SchemaVersion string
	// PreviousRelease binds the immediate predecessor, or is nil for the initial release.
	PreviousRelease *PreviousRelease
	// Source records the authoring repository and exact Git revision.
	Source Source
	// Artifacts selects one immutable revision per logical artifact identity.
	Artifacts []artifactmodel.Artifact
	// SchemaContextBindings associates selected schema revisions with selected JSON-LD context revisions.
	SchemaContextBindings []Binding
	// URICatalog resolves exact document identifiers within the selected release closure.
	URICatalog []URIEntry
	// StorageCatalog assigns canonical schemas to registered logical stores.
	StorageCatalog []StorageEntry
	// Changes optionally declares the exact machine-readable difference from PreviousRelease.
	Changes *Changes
	// Extensions preserves namespaced release-level publisher metadata.
	Extensions map[string]any
	// Raw retains a copy of the authoritative JSON Canonicalization Scheme (JCS) bytes after parsing.
	Raw []byte
}

// PreviousRelease pins the immediate predecessor by schema version and exact manifest digest.
type PreviousRelease struct {
	// SchemaVersion identifies the predecessor's published schema release.
	SchemaVersion string
	// ManifestDigest binds the predecessor's exact canonical manifest bytes.
	ManifestDigest string
}

// Source records the source-control provenance declared by the release publisher.
type Source struct {
	// Repository identifies the authoring repository through an absolute URI; validation never retrieves it.
	Repository string
	// Commit pins the authoring revision using a lowercase Git SHA-1 or SHA-256 object identifier.
	Commit string
}

// Binding associates one selected schema revision with its selected JSON-LD contexts.
type Binding struct {
	// SchemaRevision identifies the selected schema receiving these context associations.
	SchemaRevision string
	// ContextRevisions lists distinct selected context revisions in canonical lexical order.
	ContextRevisions []string
}

// URIEntry resolves one release-scoped document identifier to exact selected bytes.
type URIEntry struct {
	// URI is the exact absolute document identifier used as the catalog key.
	URI string
	// ArtifactRevision identifies the selected artifact that owns the target bytes.
	ArtifactRevision string
	// Resource names an owned resource; an empty value selects the artifact's primary payload.
	Resource string
}

// StorageEntry assigns one selected canonical schema identity to logical stores.
type StorageEntry struct {
	// Kind is schema for every version 1 storage assignment.
	Kind artifactmodel.Kind
	// ArtifactType identifies the selected canonical schema's logical type.
	ArtifactType string
	// Stores lists distinct registered logical destinations in canonical lexical order.
	Stores []Store
}

// Changes describes a release's machine-readable difference and optional publisher summary.
type Changes struct {
	// Summary contains optional human-readable release notes; it does not affect deterministic comparison.
	Summary string
	// Compatibility records the highest individual impact, or initial for the first release.
	Compatibility Compatibility
	// Artifacts lists additions, revision updates, and removals by logical artifact identity.
	Artifacts []ArtifactChange
	// SchemaContextBindings lists added or removed schema-context revision pairs.
	SchemaContextBindings []BindingChange
	// URICatalog lists added, reassigned, or removed document identifiers.
	URICatalog []URIChange
	// StorageCatalog lists added or removed schema-store associations.
	StorageCatalog []StorageChange
}

// ArtifactChange records one logical artifact's selection difference between releases.
type ArtifactChange struct {
	// Operation determines which before and after revision fields are present.
	Operation Operation `json:"operation"`
	// Kind selects the changed artifact's category.
	Kind artifactmodel.Kind `json:"kind"`
	// ArtifactType identifies the changed logical artifact within Kind.
	ArtifactType string `json:"artifactType"`
	// BeforeRevision identifies the prior revision for update and remove operations.
	BeforeRevision string `json:"beforeRevision,omitempty"`
	// AfterRevision identifies the new revision for add and update operations.
	AfterRevision string `json:"afterRevision,omitempty"`
	// Impact classifies the change's compatibility effect; removals must be breaking.
	Impact Impact `json:"impact"`
}

// BindingChange records an added or removed association between exact schema and context revisions.
type BindingChange struct {
	// Operation is add or remove; bindings do not support update.
	Operation Operation `json:"operation"`
	// SchemaRevision identifies the schema side of the changed association.
	SchemaRevision string `json:"schemaRevision"`
	// ContextRevision identifies the context side of the changed association.
	ContextRevision string `json:"contextRevision"`
	// Impact classifies the association's effect on existing interpretation.
	Impact Impact `json:"impact"`
}

// URIChange records the before and after targets of one release-catalog document identifier.
type URIChange struct {
	// Operation determines which before and after targets are present.
	Operation Operation `json:"operation"`
	// URI identifies the exact catalog key that changes.
	URI string `json:"uri"`
	// BeforeRevision identifies the previous target artifact for update and remove operations.
	BeforeRevision string `json:"beforeRevision,omitempty"`
	// BeforeResource names the previous owned resource; an empty value selects that artifact's primary payload.
	BeforeResource string `json:"beforeResource,omitempty"`
	// AfterRevision identifies the new target artifact for add and update operations.
	AfterRevision string `json:"afterRevision,omitempty"`
	// AfterResource names the new owned resource; an empty value selects that artifact's primary payload.
	AfterResource string `json:"afterResource,omitempty"`
	// Impact classifies the effect of introducing, changing, or removing this resolution.
	Impact Impact `json:"impact"`
}

// StorageChange records an added or removed logical schema-store association.
type StorageChange struct {
	// Operation is add or remove; storage associations do not support update.
	Operation Operation `json:"operation"`
	// Kind identifies the artifact category receiving the store assignment.
	Kind artifactmodel.Kind `json:"kind"`
	// ArtifactType identifies the selected schema's logical type.
	ArtifactType string `json:"artifactType"`
	// Store identifies the registered logical destination affected by the change.
	Store Store `json:"store"`
	// Impact classifies the association's effect on existing data destinations.
	Impact Impact `json:"impact"`
}

// Release records a previously validated manifest and its digest.
type Release struct {
	// Manifest contains the previously validated release selection and provenance.
	Manifest Manifest
	// Digest binds the release's authoritative canonical manifest bytes.
	Digest string
}

// Context provides caller-owned immutable bytes and predecessor lineage.
// Artifacts MUST contain exact payload and resource bytes for every selection.
type Context struct {
	// RegistryOrigin supplies the absolute origin required by every selected artifact's canonical URI.
	RegistryOrigin string
	// Artifacts supplies exact payload and resource bytes keyed by immutable identity.
	Artifacts map[artifactmodel.Dependency]artifactmodel.Artifact
	// History lists validated predecessors oldest first, ending with the immediate predecessor.
	History []Release
}
