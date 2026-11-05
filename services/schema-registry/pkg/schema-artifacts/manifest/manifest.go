// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package manifest parses and validates immutable Schema Release Manifests.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gowebpki/jcs"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	sourcemappingmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
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
	// commitPattern accepts lowercase Git object identifiers using SHA-1 or SHA-256 lengths.
	commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	// extensionPattern requires dot-separated namespace labels for publisher extension keys.
	extensionPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
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
	Artifacts []Artifact
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

// Artifact is a release selection. Bytes are supplied through Context, never JSON.
type Artifact struct {
	// Kind determines the selected artifact's format and conditional metadata requirements.
	Kind artifactmodel.Kind
	// Type identifies the case-sensitive logical artifact within Kind.
	Type string
	// Revision pins the selected immutable artifact revision.
	Revision string
	// CreatedUnderSchemaVersion records the release that first selects this revision.
	CreatedUnderSchemaVersion string
	// SchemaClass classifies schemas and remains empty for other artifact kinds.
	SchemaClass artifactmodel.SchemaClass
	// TargetSchema identifies the canonical release targeted by a source mapping; other kinds leave it empty.
	TargetSchema string
	// CanonicalURI identifies the immutable primary payload under the Registry origin.
	CanonicalURI string
	// Payload declares the exact primary payload bytes and their release-specific bundle location.
	Payload artifactmodel.Descriptor
	// Resources declares this revision's named resources without embedding their bytes in the manifest.
	Resources []artifactmodel.Resource
	// Dependencies pins external artifact references to exact revisions selected by this release.
	Dependencies []artifactmodel.Dependency
	// Extensions preserves publisher metadata bound to the selected immutable revision.
	Extensions map[string]any
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

// Key identifies one immutable artifact revision.
type Key struct {
	// Kind selects the immutable artifact's category.
	Kind artifactmodel.Kind
	// Type identifies the logical artifact within Kind.
	Type string
	// Revision pins the exact immutable revision.
	Revision string
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
	Artifacts map[Key]artifactmodel.Artifact
	// History lists validated predecessors oldest first, ending with the immediate predecessor.
	History []Release
}

// Parse strictly decodes a version 1 manifest and requires submitted bytes to be JCS.
// It checks structure, lexical forms, and ordering, but does not resolve bytes or predecessor-dependent changes.
func Parse(data []byte) (Manifest, error) {
	if len(data) > schemaartifacts.MaxArtifactBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", errInvalid)
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || bytes.HasSuffix(data, []byte("\n")) {
		return Manifest{}, fmt.Errorf("%w: manifest must not contain a byte order mark or trailing newline", errInvalid)
	}
	value, err := strictjson.Object(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return Manifest{}, fmt.Errorf("%w: manifest is not JCS canonical", errInvalid)
	}
	if validationErr := strictjson.RejectUnknownFields(value, "$schema", "formatVersion", "schemaVersion", "previousRelease", "source", "artifacts", "schemaContextBindings", "uriCatalog", "storageCatalog", "changes", "extensions"); validationErr != nil {
		return Manifest{}, fmt.Errorf("%w: manifest fields: %w", errInvalid, validationErr)
	}
	m := Manifest{Raw: append([]byte(nil), data...)}
	if m.Schema, err = requiredString(value, "$schema"); err != nil {
		return Manifest{}, err
	}
	if m.FormatVersion, err = requiredEnum[Version](value, "formatVersion"); err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion, err = requiredString(value, "schemaVersion"); err != nil {
		return Manifest{}, err
	}
	if m.PreviousRelease, err = parsePrevious(value["previousRelease"]); err != nil {
		return Manifest{}, err
	}
	if m.Source, err = parseSource(value["source"]); err != nil {
		return Manifest{}, err
	}
	if m.Artifacts, err = parseArtifacts(value["artifacts"]); err != nil {
		return Manifest{}, err
	}
	if m.SchemaContextBindings, err = parseBindings(value["schemaContextBindings"]); err != nil {
		return Manifest{}, err
	}
	if m.URICatalog, err = parseURIs(value["uriCatalog"]); err != nil {
		return Manifest{}, err
	}
	if m.StorageCatalog, err = parseStorage(value["storageCatalog"]); err != nil {
		return Manifest{}, err
	}
	if raw, ok := value["changes"]; ok {
		if m.Changes, err = parseChanges(raw); err != nil {
			return Manifest{}, err
		}
	}
	if raw, ok := value["extensions"]; ok {
		if m.Extensions, err = parseExtensions(raw); err != nil {
			return Manifest{}, err
		}
	}
	if err := m.validateStructural(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate parses data and validates its closure against context.
// It does not enforce application-specific response representation limits.
func Validate(data []byte, context Context) (Manifest, error) {
	m, err := Parse(data)
	if err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(context); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks parsed metadata, release closure, lineage, and deterministic changes.
// Registry acceptance MUST also validate application response serviceability.
func (m Manifest) Validate(context Context) error {
	if err := m.validateStructural(); err != nil {
		return err
	}
	if context.RegistryOrigin == "" {
		return fmt.Errorf("%w: registry origin is required", errInvalid)
	}
	if err := m.validateLineage(context.History); err != nil {
		return err
	}
	selected := make(map[Key]artifactmodel.Artifact, len(m.Artifacts))
	byRevision := make(map[string]Artifact, len(m.Artifacts))
	for _, selectedArtifact := range m.Artifacts {
		key := Key{Kind: selectedArtifact.Kind, Type: selectedArtifact.Type, Revision: selectedArtifact.Revision}
		bound, ok := context.Artifacts[key]
		if !ok {
			return fmt.Errorf("%w: artifact %s/%s/%s bytes are unavailable", errInvalid, key.Kind, key.Type, key.Revision)
		}
		if !matchesSelection(selectedArtifact, bound) {
			return fmt.Errorf("%w: artifact %s/%s/%s differs from immutable bytes", errInvalid, key.Kind, key.Type, key.Revision)
		}
		if validationErr := bound.Validate(context.RegistryOrigin); validationErr != nil {
			return fmt.Errorf("%w: artifact %s: %w", errInvalid, key.Type, validationErr)
		}
		if err := immutableReuse(selectedArtifact, context.History); err != nil {
			return err
		}
		if err := validateCreatedUnder(selectedArtifact, m.SchemaVersion, context.History); err != nil {
			return err
		}
		selected[key] = bound
		if previous, exists := byRevision[selectedArtifact.Revision]; exists && (previous.Kind != selectedArtifact.Kind || previous.Type != selectedArtifact.Type) {
			return fmt.Errorf("%w: artifact revisions must be release-unique", errInvalid)
		}
		byRevision[selectedArtifact.Revision] = selectedArtifact
	}
	formatCatalog := make(map[string]any, len(m.URICatalog))
	selectedByRevision := make(map[string]artifactmodel.Artifact, len(selected))
	for _, bound := range selected {
		selectedByRevision[bound.Revision] = bound
	}
	for _, entry := range m.URICatalog {
		bound, ok := selectedByRevision[entry.ArtifactRevision]
		if !ok {
			continue
		}
		if entry.Resource == "" {
			value, parseErr := strictjson.Parse(bound.PayloadBytes)
			if parseErr != nil {
				return fmt.Errorf("%w: catalog payload JSON: %w", errInvalid, parseErr)
			}
			formatCatalog[entry.URI] = value
		}
	}
	for _, bound := range selected {
		if err := bound.ValidatePayloadFormatWithCatalog(formatCatalog); err != nil {
			return fmt.Errorf("%w: validate artifact format: %w", errInvalid, err)
		}
	}
	if err := m.validateReferences(selected, byRevision); err != nil {
		return err
	}
	if err := m.validateSourceMappings(selected, context); err != nil {
		return err
	}
	if err := m.validateChanges(context); err != nil {
		return err
	}
	return nil
}

// validateStructural checks format declarations, metadata shapes, collection limits, and canonical ordering.
func (m Manifest) validateStructural() error {
	if !artifactmodel.ValidDocumentURI(m.Schema) || m.FormatVersion != FormatVersion || !artifactmodel.ValidVersion(m.SchemaVersion) {
		return fmt.Errorf("%w: invalid format declaration", errInvalid)
	}
	if !commitPattern.MatchString(m.Source.Commit) || !artifactmodel.ValidDocumentURI(m.Source.Repository) {
		return fmt.Errorf("%w: invalid source", errInvalid)
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("%w: artifacts must not be empty", errInvalid)
	}
	if err := checkCount(len(m.Artifacts)); err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		if err := validateArtifact(a); err != nil {
			return err
		}
	}
	for _, b := range m.SchemaContextBindings {
		if err := checkCount(len(b.ContextRevisions)); err != nil || b.SchemaRevision == "" {
			return fmt.Errorf("%w: invalid schema-context binding", errInvalid)
		}
	}
	for _, entry := range m.URICatalog {
		if !artifactmodel.ValidDocumentURI(entry.URI) || !artifactmodel.ValidRevision(entry.ArtifactRevision) || (entry.Resource != "" && !artifactmodel.ValidResourceName(entry.Resource)) {
			return fmt.Errorf("%w: invalid URI catalog entry", errInvalid)
		}
	}
	if m.Changes != nil {
		for _, change := range m.Changes.URICatalog {
			if !artifactmodel.ValidDocumentURI(change.URI) {
				return invalid("invalid URI change")
			}
		}
	}
	for _, entry := range m.StorageCatalog {
		if entry.Kind != artifactmodel.KindSchema || !artifactmodel.ValidLogicalType(entry.ArtifactType) || len(entry.Stores) == 0 || checkCount(len(entry.Stores)) != nil {
			return fmt.Errorf("%w: invalid storage catalog entry", errInvalid)
		}
		for _, store := range entry.Stores {
			if !validStore(store) {
				return fmt.Errorf("%w: invalid store", errInvalid)
			}
		}
	}
	return m.validateOrder()
}

// validateLineage binds the immediate predecessor and rejects repeated schema versions in the supplied history.
func (m Manifest) validateLineage(history []Release) error {
	if len(history) == 0 {
		if m.PreviousRelease != nil {
			return fmt.Errorf("%w: initial release must have null predecessor", errInvalid)
		}
		return nil
	}
	previous := history[len(history)-1]
	if m.PreviousRelease == nil || m.PreviousRelease.SchemaVersion != previous.Manifest.SchemaVersion || m.PreviousRelease.ManifestDigest != previous.Digest {
		return fmt.Errorf("%w: predecessor does not bind immediate release", errInvalid)
	}
	seen := make(map[string]struct{}, len(history))
	for _, release := range history {
		if _, ok := seen[release.Manifest.SchemaVersion]; ok {
			return fmt.Errorf("%w: non-linear release lineage", errInvalid)
		}
		seen[release.Manifest.SchemaVersion] = struct{}{}
	}
	if _, exists := seen[m.SchemaVersion]; exists {
		return fmt.Errorf("%w: schema version already exists in lineage", errInvalid)
	}
	return nil
}

// validateReferences enforces unique selections, dependency closure, context bindings, URI ownership, and class-appropriate store placement.
func (m Manifest) validateReferences(selected map[Key]artifactmodel.Artifact, revisions map[string]Artifact) error {
	logical := make(map[string]struct{}, len(m.Artifacts))
	canonical := make(map[string]struct{}, len(m.Artifacts))
	for _, item := range m.Artifacts {
		identity := string(item.Kind) + "\x00" + item.Type
		if _, ok := logical[identity]; ok {
			return fmt.Errorf("%w: duplicate artifact selection", errInvalid)
		}
		logical[identity] = struct{}{}
		if _, ok := canonical[item.CanonicalURI]; ok {
			return fmt.Errorf("%w: duplicate canonical URI", errInvalid)
		}
		canonical[item.CanonicalURI] = struct{}{}
		for _, dep := range item.Dependencies {
			if _, ok := selected[Key{dep.Kind, dep.Type, dep.Revision}]; !ok {
				return fmt.Errorf("%w: unresolved dependency", errInvalid)
			}
		}
	}
	bindings := make(map[string]struct{}, len(m.SchemaContextBindings))
	for _, binding := range m.SchemaContextBindings {
		schema, ok := revisions[binding.SchemaRevision]
		if !ok || schema.Kind != artifactmodel.KindSchema {
			return fmt.Errorf("%w: binding schema is unresolved", errInvalid)
		}
		if _, exists := bindings[binding.SchemaRevision]; exists {
			return fmt.Errorf("%w: duplicate schema binding", errInvalid)
		}
		bindings[binding.SchemaRevision] = struct{}{}
		contexts := make(map[string]struct{}, len(binding.ContextRevisions))
		for _, revision := range binding.ContextRevisions {
			context, ok := revisions[revision]
			if !ok || context.Kind != artifactmodel.KindContext {
				return fmt.Errorf("%w: binding context is unresolved", errInvalid)
			}
			if _, exists := contexts[revision]; exists {
				return fmt.Errorf("%w: duplicate context binding", errInvalid)
			}
			contexts[revision] = struct{}{}
		}
	}
	uris := make(map[string]struct{}, len(m.URICatalog))
	for _, entry := range m.URICatalog {
		target, ok := revisions[entry.ArtifactRevision]
		if !ok {
			return fmt.Errorf("%w: URI target is unresolved", errInvalid)
		}
		if entry.Resource != "" && !hasResource(target, entry.Resource) {
			return fmt.Errorf("%w: URI resource target is unresolved", errInvalid)
		}
		if _, exists := uris[entry.URI]; exists {
			return fmt.Errorf("%w: duplicate URI", errInvalid)
		}
		uris[entry.URI] = struct{}{}
	}
	for uri := range canonical {
		if _, ok := uris[uri]; !ok {
			return fmt.Errorf("%w: missing canonical URI catalog entry", errInvalid)
		}
	}
	for _, item := range m.Artifacts {
		entry, ok := findURIEntry(m.URICatalog, item.CanonicalURI)
		if !ok || entry.ArtifactRevision != item.Revision || entry.Resource != "" {
			return fmt.Errorf("%w: canonical URI must resolve to its artifact payload", errInvalid)
		}
	}
	for _, item := range m.Artifacts {
		bound := selected[Key{item.Kind, item.Type, item.Revision}]
		for _, required := range requiredURIs(bound) {
			if _, ok := uris[required]; !ok {
				return fmt.Errorf("%w: missing required URI catalog entry %q", errInvalid, required)
			}
		}
	}
	placements := make(map[string]struct{}, len(m.StorageCatalog))
	for _, entry := range m.StorageCatalog {
		selectedSchema, ok := findSelected(m.Artifacts, entry.Kind, entry.ArtifactType)
		if !ok || selectedSchema.SchemaClass == artifactmodel.SchemaClassSource {
			return fmt.Errorf("%w: invalid schema storage placement", errInvalid)
		}
		key := string(entry.Kind) + "\x00" + entry.ArtifactType
		if _, exists := placements[key]; exists {
			return fmt.Errorf("%w: duplicate storage placement", errInvalid)
		}
		placements[key] = struct{}{}
	}
	for _, item := range m.Artifacts {
		if item.Kind != artifactmodel.KindSchema {
			continue
		}
		_, placed := placements[string(item.Kind)+"\x00"+item.Type]
		if (item.SchemaClass == artifactmodel.SchemaClassSource) == placed {
			return fmt.Errorf("%w: schema storage placement does not agree with class", errInvalid)
		}
	}
	return nil
}

// validateSourceMappings resolves selected mapping descriptors against the containing release and its supplied ancestors.
func (m Manifest) validateSourceMappings(selected map[Key]artifactmodel.Artifact, context Context) error {
	schemas := make([]sourcemappingmodel.Schema, 0)
	for _, item := range m.Artifacts {
		if item.Kind == artifactmodel.KindSchema {
			schemas = append(schemas, sourcemappingmodel.Schema{Type: item.Type, Revision: item.Revision, Class: item.SchemaClass})
		}
	}
	lineage := []sourcemappingmodel.Release{{Version: m.SchemaVersion, Schemas: schemas}}
	for _, v := range slices.Backward(context.History) {
		prior := v.Manifest
		priorSchemas := make([]sourcemappingmodel.Schema, 0)
		for _, item := range prior.Artifacts {
			if item.Kind == artifactmodel.KindSchema {
				priorSchemas = append(priorSchemas, sourcemappingmodel.Schema{Type: item.Type, Revision: item.Revision, Class: item.SchemaClass})
			}
		}
		lineage = append(lineage, sourcemappingmodel.Release{Version: prior.SchemaVersion, Schemas: priorSchemas})
	}
	mappings := make([]artifactmodel.Artifact, 0)
	for _, item := range m.Artifacts {
		if item.Kind == artifactmodel.KindSourceMapping {
			mappings = append(mappings, selected[Key{item.Kind, item.Type, item.Revision}])
		}
	}
	_, err := sourcemappingmodel.ValidateRelease(mappings, sourcemappingmodel.ValidationContext{Version: m.SchemaVersion, Lineage: lineage})
	if err != nil {
		return fmt.Errorf("%w: source mapping agreement: %w", errInvalid, err)
	}
	return nil
}

// validateChanges checks initial-release summaries, effective differences, exact predecessor changes, and required version increments.
func (m Manifest) validateChanges(context Context) error {
	if m.Changes == nil {
		if len(context.History) > 0 {
			diff, err := Diff(context.History[len(context.History)-1].Manifest, m, context)
			if err != nil {
				return err
			}
			if diff.Compatibility == CompatibilityOperational && len(diff.Artifacts) == 0 && len(diff.SchemaContextBindings) == 0 && len(diff.URICatalog) == 0 && len(diff.StorageCatalog) == 0 {
				return fmt.Errorf("%w: release has no effective difference", errInvalid)
			}
		}
		return nil
	}
	if len(context.History) == 0 {
		if m.Changes.Compatibility != CompatibilityInitial || len(m.Changes.Artifacts)+len(m.Changes.SchemaContextBindings)+len(m.Changes.URICatalog)+len(m.Changes.StorageCatalog) != 0 {
			return fmt.Errorf("%w: invalid initial change summary", errInvalid)
		}
		return nil
	}
	expected, err := Diff(context.History[len(context.History)-1].Manifest, m, context)
	if err != nil {
		return err
	}
	if len(expected.Artifacts)+len(expected.SchemaContextBindings)+len(expected.URICatalog)+len(expected.StorageCatalog) == 0 {
		return fmt.Errorf("%w: release has no effective difference", errInvalid)
	}
	if !equalChanges(*m.Changes, expected) {
		return fmt.Errorf("%w: changes do not equal deterministic difference", errInvalid)
	}
	if !validIncrement(context.History[len(context.History)-1].Manifest.SchemaVersion, m.SchemaVersion, expected.Compatibility) {
		return fmt.Errorf("%w: schema version is not the required compatibility increment", errInvalid)
	}
	return nil
}

// Diff returns the deterministic conservative change summary for two manifests.
// The caller supplies validated endpoint manifests and exact bytes needed to prove annotation-only updates or additive mapping additions.
func Diff(before, after Manifest, context Context) (Changes, error) {
	changes := Changes{Artifacts: make([]ArtifactChange, 0)}
	beforeArtifacts, afterArtifacts := artifactMap(before), artifactMap(after)
	keys := unionKeys(beforeArtifacts, afterArtifacts)
	for _, key := range keys {
		left, hasLeft := beforeArtifacts[key]
		right, hasRight := afterArtifacts[key]
		switch {
		case !hasLeft:
			impact := ImpactAdditive
			if right.Kind == artifactmodel.KindSourceMapping {
				impact = addedMappingImpact(before, right, context)
			}
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: OperationAdd, Kind: right.Kind, ArtifactType: right.Type, AfterRevision: right.Revision, Impact: impact})
		case !hasRight:
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: OperationRemove, Kind: left.Kind, ArtifactType: left.Type, BeforeRevision: left.Revision, Impact: ImpactBreaking})
		case left.Revision != right.Revision:
			impact := ImpactBreaking
			if left.Kind == artifactmodel.KindSchema && annotationOnly(left, right, context) {
				impact = ImpactOperational
			}
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: OperationUpdate, Kind: right.Kind, ArtifactType: right.Type, BeforeRevision: left.Revision, AfterRevision: right.Revision, Impact: impact})
		}
	}
	changes.SchemaContextBindings = diffBindings(before, after)
	changes.URICatalog = diffURIs(before, after)
	changes.StorageCatalog = diffStorage(before, after)
	if changes.SchemaContextBindings == nil {
		changes.SchemaContextBindings = []BindingChange{}
	}
	if changes.URICatalog == nil {
		changes.URICatalog = []URIChange{}
	}
	if changes.StorageCatalog == nil {
		changes.StorageCatalog = []StorageChange{}
	}
	changes.Compatibility = classify(changes)
	sortChanges(&changes)
	return changes, nil
}

// addedMappingImpact permits additive mappings only when their descriptor proves the source schema is newly introduced.
func addedMappingImpact(before Manifest, added Artifact, context Context) Impact {
	bound, ok := context.Artifacts[Key{Kind: added.Kind, Type: added.Type, Revision: added.Revision}]
	if !ok {
		return ImpactBreaking
	}
	descriptor, err := sourcemappingmodel.Parse(bound.PayloadBytes)
	if err != nil {
		return ImpactBreaking
	}
	if _, exists := findSelected(before.Artifacts, artifactmodel.KindSchema, descriptor.SourceSchema.ID); exists {
		return ImpactBreaking
	}
	return ImpactAdditive
}

// parsePrevious accepts null or an exact predecessor object with valid version and digest spellings.
func parsePrevious(raw any) (*PreviousRelease, error) {
	if raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("previousRelease must be null or an object")
	}
	if err := strictjson.RejectUnknownFields(object, "schemaVersion", "manifestDigest"); err != nil {
		return nil, err
	}
	version, err := requiredString(object, "schemaVersion")
	if err != nil {
		return nil, err
	}
	digest, err := requiredString(object, "manifestDigest")
	if err != nil {
		return nil, err
	}
	if !artifactmodel.ValidVersion(version) || !artifactmodel.ValidDigest(digest) {
		return nil, invalid("invalid previous release")
	}
	return &PreviousRelease{version, digest}, nil
}

// parseSource decodes the required repository and commit fields while rejecting unknown provenance fields.
func parseSource(raw any) (Source, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return Source{}, invalid("source must be an object")
	}
	if err := strictjson.RejectUnknownFields(object, "repository", "commit"); err != nil {
		return Source{}, err
	}
	repository, err := requiredString(object, "repository")
	if err != nil {
		return Source{}, err
	}
	commit, err := requiredString(object, "commit")
	if err != nil {
		return Source{}, err
	}
	return Source{repository, commit}, nil
}

// parseArtifacts decodes bounded release selections and rejects unknown artifact fields before structural validation.
func parseArtifacts(raw any) ([]Artifact, error) {
	values, err := array(raw, "artifacts")
	if err != nil {
		return nil, err
	}
	result := make([]Artifact, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("artifact must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "schemaClass", "targetSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "extensions"); validationErr != nil {
			return nil, validationErr
		}
		item := Artifact{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.Type, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Revision, err = requiredString(object, "artifactRevision"); err != nil {
			return nil, err
		}
		if item.CreatedUnderSchemaVersion, err = requiredString(object, "createdUnderSchemaVersion"); err != nil {
			return nil, err
		}
		if item.CanonicalURI, err = requiredString(object, "canonicalUri"); err != nil {
			return nil, err
		}
		if item.SchemaClass, err = optionalSchemaClass(object, "schemaClass"); err != nil {
			return nil, err
		}
		if item.TargetSchema, err = optionalString(object, "targetSchemaVersion"); err != nil {
			return nil, err
		}
		if item.Payload, err = parseDescriptor(object["payload"]); err != nil {
			return nil, err
		}
		if item.Resources, err = parseResources(object["resources"]); err != nil {
			return nil, err
		}
		if item.Dependencies, err = parseDependencies(object["dependencies"]); err != nil {
			return nil, err
		}
		if value, ok := object["extensions"]; ok {
			if item.Extensions, err = parseExtensions(value); err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// parseDescriptor decodes byte metadata and a bundle path without loading or hashing the described bytes.
func parseDescriptor(raw any) (artifactmodel.Descriptor, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return artifactmodel.Descriptor{}, invalid("descriptor must be object")
	}
	if err := strictjson.RejectUnknownFields(object, "mediaType", "digest", "size", "bundlePath"); err != nil {
		return artifactmodel.Descriptor{}, err
	}
	media, err := requiredString(object, "mediaType")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	digest, err := requiredString(object, "digest")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	path, err := requiredString(object, "bundlePath")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	size, err := number(object, "size")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	return artifactmodel.Descriptor{MediaType: media, Digest: digest, Size: size, BundlePath: path}, nil
}

// parseResources decodes bounded named-resource metadata without retaining resource bytes.
func parseResources(raw any) ([]artifactmodel.Resource, error) {
	values, err := array(raw, "resources")
	if err != nil {
		return nil, err
	}
	result := make([]artifactmodel.Resource, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("resource must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "name", "role", "mediaType", "digest", "size", "bundlePath"); err != nil {
			return nil, err
		}
		name, err := requiredString(object, "name")
		if err != nil {
			return nil, err
		}
		role, err := requiredString(object, "role")
		if err != nil {
			return nil, err
		}
		descriptor, err := parseResourceDescriptor(object)
		if err != nil {
			return nil, err
		}
		result = append(result, artifactmodel.Resource{Name: name, Role: artifactmodel.ResourceRole(role), Descriptor: descriptor})
	}
	return result, nil
}

// parseResourceDescriptor extracts only byte-descriptor fields from a resource object containing additional name and role fields.
func parseResourceDescriptor(object map[string]any) (artifactmodel.Descriptor, error) {
	return parseDescriptor(map[string]any{
		"mediaType":  object["mediaType"],
		"digest":     object["digest"],
		"size":       object["size"],
		"bundlePath": object["bundlePath"],
	})
}

// parseDependencies decodes bounded exact-revision references with registered artifact kinds.
func parseDependencies(raw any) ([]artifactmodel.Dependency, error) {
	values, err := array(raw, "dependencies")
	if err != nil {
		return nil, err
	}
	result := make([]artifactmodel.Dependency, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("dependency must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision"); validationErr != nil {
			return nil, validationErr
		}
		item := artifactmodel.Dependency{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.Type, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Revision, err = requiredString(object, "artifactRevision"); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// parseBindings decodes bounded schema-context associations and requires at least one context per binding.
func parseBindings(raw any) ([]Binding, error) {
	values, err := array(raw, "schemaContextBindings")
	if err != nil {
		return nil, err
	}
	result := make([]Binding, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("binding must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "schemaRevision", "contextRevisions"); err != nil {
			return nil, err
		}
		schema, err := requiredString(object, "schemaRevision")
		if err != nil {
			return nil, err
		}
		contexts, err := stringsArray(object["contextRevisions"], "contextRevisions")
		if err != nil {
			return nil, err
		}
		if len(contexts) == 0 {
			return nil, invalid("context revisions must not be empty")
		}
		result = append(result, Binding{schema, contexts})
	}
	return result, nil
}

// parseURIs decodes bounded document-resolution entries, preserving an absent resource selector as the primary payload.
func parseURIs(raw any) ([]URIEntry, error) {
	values, err := array(raw, "uriCatalog")
	if err != nil {
		return nil, err
	}
	result := make([]URIEntry, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("URI entry must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "uri", "artifactRevision", "resource"); err != nil {
			return nil, err
		}
		uri, err := requiredString(object, "uri")
		if err != nil {
			return nil, err
		}
		revision, err := requiredString(object, "artifactRevision")
		if err != nil {
			return nil, err
		}
		resource, err := optionalString(object, "resource")
		if err != nil {
			return nil, err
		}
		result = append(result, URIEntry{uri, revision, resource})
	}
	return result, nil
}

// parseStorage decodes bounded logical schema-store assignments before registered-store and placement validation.
func parseStorage(raw any) ([]StorageEntry, error) {
	values, err := array(raw, "storageCatalog")
	if err != nil {
		return nil, err
	}
	result := make([]StorageEntry, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("storage entry must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "stores"); validationErr != nil {
			return nil, validationErr
		}
		item := StorageEntry{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		stores, err := stringsArray(object["stores"], "stores")
		if err != nil {
			return nil, err
		}
		item.Stores = make([]Store, 0, len(stores))
		for _, store := range stores {
			item.Stores = append(item.Stores, Store(store))
		}
		result = append(result, item)
	}
	return result, nil
}

// parseChanges decodes a complete change summary and validates its compatibility value and individual change shapes.
func parseChanges(raw any) (*Changes, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("changes must be an object")
	}
	if err := strictjson.RejectUnknownFields(object, "summary", "compatibility", "artifacts", "schemaContextBindings", "uriCatalog", "storageCatalog"); err != nil {
		return nil, err
	}
	changes := &Changes{}
	var err error
	if changes.Summary, err = optionalString(object, "summary"); err != nil {
		return nil, err
	}
	if changes.Compatibility, err = requiredEnum[Compatibility](object, "compatibility"); err != nil {
		return nil, err
	}
	if changes.Artifacts, err = parseArtifactChanges(object["artifacts"]); err != nil {
		return nil, err
	}
	if changes.SchemaContextBindings, err = parseBindingChanges(object["schemaContextBindings"]); err != nil {
		return nil, err
	}
	if changes.URICatalog, err = parseURIChanges(object["uriCatalog"]); err != nil {
		return nil, err
	}
	if changes.StorageCatalog, err = parseStorageChanges(object["storageCatalog"]); err != nil {
		return nil, err
	}
	if changes.Compatibility != CompatibilityInitial && changes.Compatibility != CompatibilityBreaking && changes.Compatibility != CompatibilityAdditive && changes.Compatibility != CompatibilityOperational {
		return nil, invalid("invalid changes compatibility")
	}
	return changes, nil
}

// parseArtifactChanges enforces registered kinds, supported impacts, and operation-specific revision fields.
func parseArtifactChanges(raw any) ([]ArtifactChange, error) {
	values, err := array(raw, "artifact changes")
	if err != nil {
		return nil, err
	}
	result := make([]ArtifactChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("artifact change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "beforeRevision", "afterRevision", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := ArtifactChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.BeforeRevision, err = optionalString(object, "beforeRevision"); err != nil {
			return nil, err
		}
		if item.AfterRevision, err = optionalString(object, "afterRevision"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if err := validateArtifactChange(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// parseBindingChanges accepts bounded add or remove entries with supported impacts and non-empty revision selectors.
func parseBindingChanges(raw any) ([]BindingChange, error) {
	values, err := array(raw, "binding changes")
	if err != nil {
		return nil, err
	}
	result := make([]BindingChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("binding change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "schemaRevision", "contextRevision", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := BindingChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.SchemaRevision, err = requiredString(object, "schemaRevision"); err != nil {
			return nil, err
		}
		if item.ContextRevision, err = requiredString(object, "contextRevision"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != OperationAdd && item.Operation != OperationRemove) || !validImpact(item.Impact) {
			return nil, invalid("invalid binding change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseURIChanges checks absolute identifiers, revision transitions, optional resource names, and supported impacts.
func parseURIChanges(raw any) ([]URIChange, error) {
	values, err := array(raw, "URI changes")
	if err != nil {
		return nil, err
	}
	result := make([]URIChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("URI change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "uri", "beforeRevision", "beforeResource", "afterRevision", "afterResource", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := URIChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.URI, err = requiredString(object, "uri"); err != nil {
			return nil, err
		}
		if item.BeforeRevision, err = optionalString(object, "beforeRevision"); err != nil {
			return nil, err
		}
		if item.BeforeResource, err = optionalString(object, "beforeResource"); err != nil {
			return nil, err
		}
		if item.AfterRevision, err = optionalString(object, "afterRevision"); err != nil {
			return nil, err
		}
		if item.AfterResource, err = optionalString(object, "afterResource"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if !artifactmodel.ValidDocumentURI(item.URI) || !validChangeShape(item.Operation, item.BeforeRevision, item.AfterRevision) || !validImpact(item.Impact) || (item.BeforeResource != "" && !artifactmodel.ValidResourceName(item.BeforeResource)) || (item.AfterResource != "" && !artifactmodel.ValidResourceName(item.AfterResource)) {
			return nil, invalid("invalid URI change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseStorageChanges accepts bounded add or remove entries with registered stores, artifact kinds, and supported impacts.
func parseStorageChanges(raw any) ([]StorageChange, error) {
	values, err := array(raw, "storage changes")
	if err != nil {
		return nil, err
	}
	result := make([]StorageChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("storage change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "store", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := StorageChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Store, err = requiredEnum[Store](object, "store"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != OperationAdd && item.Operation != OperationRemove) || !validStore(item.Store) || !validImpact(item.Impact) {
			return nil, invalid("invalid storage change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseExtensions requires a bounded object whose publisher-defined keys use dot-separated namespace labels.
func parseExtensions(raw any) (map[string]any, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("extensions must be object")
	}
	if err := checkCount(len(object)); err != nil {
		return nil, err
	}
	for name := range object {
		if !extensionPattern.MatchString(name) {
			return nil, invalid("invalid extension name")
		}
	}
	return object, nil
}

// requiredString retrieves a present, non-empty JSON string without applying field-specific lexical rules.
func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return value, nil
}

// requiredEnum decodes a string; callers validate its field-specific allowed values.
func requiredEnum[T ~string](object map[string]any, name string) (T, error) {
	value, err := requiredString(object, name)
	return T(value), err
}

// optionalString returns empty for an absent field and rejects present empty or non-string values.
func optionalString(object map[string]any, name string) (string, error) {
	value, ok := object[name]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return text, nil
}

// array requires a JSON array within the manifest collection limit and names the field in shape errors.
func array(raw any, name string) ([]any, error) {
	value, ok := raw.([]any)
	if !ok {
		return nil, invalid(name + " must be array")
	}
	if err := checkCount(len(value)); err != nil {
		return nil, err
	}
	return value, nil
}

// stringsArray decodes a bounded array of non-empty strings without applying element-specific lexical rules.
func stringsArray(raw any, name string) ([]string, error) {
	values, err := array(raw, name)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, invalid(name + " must contain non-empty strings")
		}
		result = append(result, text)
	}
	return result, nil
}

// kind retrieves a required string and rejects values outside the version 1 artifact-kind registry.
func kind(object map[string]any, name string) (artifactmodel.Kind, error) {
	value, err := requiredString(object, name)
	if err != nil {
		return "", err
	}
	kind := artifactmodel.Kind(value)
	if kind != artifactmodel.KindSchema && kind != artifactmodel.KindContext && kind != artifactmodel.KindSourceMapping {
		return "", invalid("invalid artifact kind")
	}
	return kind, nil
}

// optionalSchemaClass decodes an optional classification; artifact-kind validation checks its allowed values and presence.
func optionalSchemaClass(object map[string]any, name string) (artifactmodel.SchemaClass, error) {
	value, err := optionalString(object, name)
	if err != nil {
		return "", err
	}
	return artifactmodel.SchemaClass(value), nil
}

// number retrieves a required non-negative integer from a strict JSON number representation.
func number(object map[string]any, name string) (int64, error) {
	raw, ok := object[name]
	if !ok {
		return 0, invalid(name + " is required")
	}
	value, ok := raw.(interface{ String() string })
	if !ok {
		return 0, invalid(name + " must be number")
	}
	number, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || number < 0 {
		return 0, invalid("invalid size")
	}
	return number, nil
}

// invalid attaches the manifest validation sentinel to a field-specific diagnostic.
func invalid(message string) error { return fmt.Errorf("%w: %s", errInvalid, message) }

// checkCount rejects collections larger than the version 1 manifest entry limit.
func checkCount(count int) error {
	if count > schemaartifacts.MaxArrayEntries {
		return invalid("collection exceeds entry limit")
	}
	return nil
}

// absoluteURI recognizes document identifiers for catalog coverage, including invalid over-limit identifiers.
// Recognition MUST NOT silently omit identifiers that bounded catalog validation rejects.
func absoluteURI(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.RawQuery == "" && parsed.Fragment == ""
}

// validStore reports whether value names one of the four registered logical stores.
func validStore(value Store) bool {
	switch value {
	case StoreDataCenterInventory, StoreTelemetryStore, StoreInsightsRepository, StoreRecordedActionsRepository:
		return true
	}
	return false
}

// validImpact reports whether value is an individual change impact, excluding initial-release compatibility.
func validImpact(value Impact) bool {
	return value == ImpactBreaking || value == ImpactAdditive || value == ImpactOperational
}

// validateArtifact checks selection metadata and collection bounds without resolving or verifying the selected bytes.
func validateArtifact(item Artifact) error {
	if !artifactmodel.ValidLogicalType(item.Type) || !artifactmodel.ValidRevision(item.Revision) || !artifactmodel.ValidVersion(item.CreatedUnderSchemaVersion) || !artifactmodel.ValidDocumentURI(item.CanonicalURI) {
		return invalid("invalid artifact identity")
	}
	if !artifactmodel.ValidDigest(item.Payload.Digest) || item.Payload.Size < 0 || !artifactmodel.ValidBundlePath(item.Payload.BundlePath) {
		return invalid("invalid payload descriptor")
	}
	if item.Kind == artifactmodel.KindSchema {
		if item.SchemaClass != artifactmodel.SchemaClassEntity && item.SchemaClass != artifactmodel.SchemaClassIdentifier && item.SchemaClass != artifactmodel.SchemaClassEdge && item.SchemaClass != artifactmodel.SchemaClassSource || item.TargetSchema != "" {
			return invalid("invalid schema fields")
		}
	} else if item.Kind == artifactmodel.KindContext {
		if item.SchemaClass != "" || item.TargetSchema != "" {
			return invalid("invalid context fields")
		}
	} else if item.SchemaClass != "" || !artifactmodel.ValidVersion(item.TargetSchema) {
		return invalid("invalid source mapping fields")
	}
	if err := checkCount(len(item.Resources)); err != nil {
		return err
	}
	if err := checkCount(len(item.Dependencies)); err != nil {
		return err
	}
	return nil
}

// validateArtifactChange checks logical identity, revision transition shape, supported impact, and breaking removals.
func validateArtifactChange(item ArtifactChange) error {
	if !artifactmodel.ValidLogicalType(item.ArtifactType) || !validImpact(item.Impact) || !validChangeShape(item.Operation, item.BeforeRevision, item.AfterRevision) {
		return invalid("invalid artifact change")
	}
	if item.Operation == OperationRemove && item.Impact != ImpactBreaking {
		return invalid("artifact removal must be breaking")
	}
	return nil
}

// validChangeShape checks required and absent revision selectors for add, update, and remove operations.
func validChangeShape(operation Operation, before, after string) bool {
	switch operation {
	case OperationAdd:
		return before == "" && artifactmodel.ValidRevision(after)
	case OperationUpdate:
		return artifactmodel.ValidRevision(before) && artifactmodel.ValidRevision(after) && before != after
	case OperationRemove:
		return artifactmodel.ValidRevision(before) && after == ""
	}
	return false
}

// matchesSelection compares manifest metadata with caller-supplied artifact metadata, excluding embedded resource bytes.
func matchesSelection(selection Artifact, bound artifactmodel.Artifact) bool {
	if selection.Kind != bound.Kind || selection.Type != bound.Type || selection.Revision != bound.Revision || selection.CreatedUnderSchemaVersion != bound.CreatedUnder || selection.SchemaClass != bound.SchemaClass || selection.TargetSchema != bound.TargetSchema || selection.CanonicalURI != bound.CanonicalURI || !reflect.DeepEqual(selection.Payload, bound.Payload) || !sameResources(selection.Resources, stripResourceBytes(bound.Resources)) || !sameDependencies(selection.Dependencies, bound.Dependencies) || !sameExtensions(selection.Extensions, bound.Extensions) {
		return false
	}
	return true
}

// sameResources compares ordered resource values, including the distinction between nil and empty slices.
func sameResources(left, right []artifactmodel.Resource) bool {
	return len(left) == len(right) && reflect.DeepEqual(left, right)
}

// sameDependencies compares ordered dependency identities while treating nil and empty slices as equal.
func sameDependencies(left, right []artifactmodel.Dependency) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// sameExtensions deeply compares extension keys, values, and nil-map representation.
func sameExtensions(left, right map[string]any) bool {
	return len(left) == len(right) && reflect.DeepEqual(left, right)
}

// stripResourceBytes copies resource descriptors and clears bytes without modifying the caller's resource slice.
func stripResourceBytes(resources []artifactmodel.Resource) []artifactmodel.Resource {
	result := make([]artifactmodel.Resource, len(resources))
	copy(result, resources)
	for index := range result {
		result[index].Bytes = nil
	}
	return result
}

// immutableReuse rejects changed revision metadata, reassigned canonical identifiers, and reuse of retired logical identities.
func immutableReuse(selection Artifact, history []Release) error {
	seenIdentity, retired := false, false
	for _, release := range history {
		present := false
		for _, previous := range release.Manifest.Artifacts {
			if previous.Kind == selection.Kind && previous.Type == selection.Type {
				present = true
				seenIdentity = true
			}
			if previous.CanonicalURI == selection.CanonicalURI && previous.Revision != selection.Revision {
				return fmt.Errorf("%w: historical canonical URI is reassigned", errInvalid)
			}
			if previous.Kind == selection.Kind && previous.Type == selection.Type && previous.Revision == selection.Revision && !reflect.DeepEqual(revisionBoundSelection(previous), revisionBoundSelection(selection)) {
				return fmt.Errorf("%w: immutable revision changed", errInvalid)
			}
		}
		if seenIdentity && !present {
			retired = true
		}
	}
	if retired {
		return fmt.Errorf("%w: retired logical identity is reused", errInvalid)
	}
	return nil
}

// revisionBoundSelection copies selection metadata while removing release-specific payload and resource bundle paths.
func revisionBoundSelection(value Artifact) Artifact {
	value.Payload.BundlePath = ""
	value.Resources = append([]artifactmodel.Resource(nil), value.Resources...)
	for index := range value.Resources {
		value.Resources[index].BundlePath = ""
	}
	return value
}

// validateCreatedUnder preserves a reused revision's creation release and binds new revisions to the containing release.
func validateCreatedUnder(selection Artifact, version string, history []Release) error {
	for _, release := range history {
		for _, prior := range release.Manifest.Artifacts {
			if prior.Kind == selection.Kind && prior.Type == selection.Type && prior.Revision == selection.Revision {
				if selection.CreatedUnderSchemaVersion != prior.CreatedUnderSchemaVersion {
					return fmt.Errorf("%w: reused revision changed creation version", errInvalid)
				}
				return nil
			}
		}
	}
	if selection.CreatedUnderSchemaVersion != version {
		return fmt.Errorf("%w: first selected revision must be created under this release", errInvalid)
	}
	return nil
}

// findSelected looks up a selected artifact by its kind and case-sensitive logical type.
func findSelected(items []Artifact, kind artifactmodel.Kind, artifactType string) (Artifact, bool) {
	for _, item := range items {
		if item.Kind == kind && item.Type == artifactType {
			return item, true
		}
	}
	return Artifact{}, false
}

// hasResource reports whether the selection declares an owned resource with the exact requested name.
func hasResource(item Artifact, name string) bool {
	for _, resource := range item.Resources {
		if resource.Name == name {
			return true
		}
	}
	return false
}

// findURIEntry looks up an exact catalog identifier without normalization or external resolution.
func findURIEntry(entries []URIEntry, uri string) (URIEntry, bool) {
	for _, entry := range entries {
		if entry.URI == uri {
			return entry, true
		}
	}
	return URIEntry{}, false
}

// requiredURIs collects the canonical payload identifier and supported top-level document identifiers from the exact payload.
func requiredURIs(item artifactmodel.Artifact) []string {
	result := []string{item.CanonicalURI}
	value, err := strictjson.Parse(item.PayloadBytes)
	if err != nil {
		return result
	}
	object, ok := value.(map[string]any)
	if !ok {
		return result
	}
	if identifier, ok := object["$id"].(string); ok && absoluteURI(identifier) {
		result = append(result, identifier)
	}
	if context, ok := object["@context"].(string); ok && absoluteURI(context) {
		result = append(result, context)
	}
	return result
}

// validateOrder checks strict UTF-16 lexical ordering for artifact, resource, catalog, binding, and change collections.
func (m Manifest) validateOrder() error {
	if !sorted(m.Artifacts, func(left, right Artifact) int {
		return compareMany(string(left.Kind), left.Type, string(right.Kind), right.Type)
	}) {
		return invalid("artifacts are not ordered")
	}
	for _, item := range m.Artifacts {
		if !sorted(item.Resources, func(left, right artifactmodel.Resource) int {
			return compareMany(string(left.Role), left.Name, string(right.Role), right.Name)
		}) || !sorted(item.Dependencies, func(left, right artifactmodel.Dependency) int {
			return compareMany(string(left.Kind), left.Type, left.Revision, string(right.Kind), right.Type, right.Revision)
		}) {
			return invalid("artifact arrays are not ordered")
		}
	}
	if !sorted(m.SchemaContextBindings, func(left, right Binding) int { return compareUTF16(left.SchemaRevision, right.SchemaRevision) }) {
		return invalid("bindings are not ordered")
	}
	for _, binding := range m.SchemaContextBindings {
		if !sorted(binding.ContextRevisions, compareUTF16) {
			return invalid("binding contexts are not ordered")
		}
	}
	if !sorted(m.URICatalog, func(left, right URIEntry) int { return compareUTF16(left.URI, right.URI) }) || !sorted(m.StorageCatalog, func(left, right StorageEntry) int {
		return compareMany(string(left.Kind), left.ArtifactType, string(right.Kind), right.ArtifactType)
	}) {
		return invalid("catalogs are not ordered")
	}
	for _, entry := range m.StorageCatalog {
		if !sorted(entry.Stores, func(left, right Store) int { return compareUTF16(string(left), string(right)) }) {
			return invalid("stores are not ordered")
		}
	}
	if m.Changes != nil {
		changes := *m.Changes
		sortedChanges := changes
		sortChanges(&sortedChanges)
		if !reflect.DeepEqual(changes, sortedChanges) {
			return invalid("change arrays are not ordered")
		}
	}
	return nil
}

// sorted requires each adjacent pair to compare strictly increasing, rejecting duplicates as well as descending values.
func sorted[T any](values []T, compare func(T, T) int) bool {
	for index := 1; index < len(values); index++ {
		if compare(values[index-1], values[index]) >= 0 {
			return false
		}
	}
	return true
}

// compareMany compares two equally sized tuples, supplied as all left fields followed by all right fields.
// It panics when the argument count is odd.
func compareMany(values ...string) int {
	if len(values)%2 != 0 {
		panic("compareMany requires left and right fields")
	}
	half := len(values) / 2
	for index := range half {
		if comparison := compareUTF16(values[index], values[index+half]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

// compareUTF16 compares strings by UTF-16 code units, as required by the manifest's canonical array ordering.
func compareUTF16[T ~string](left, right T) int {
	leftUnits, rightUnits := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] < rightUnits[index] {
			return -1
		}
		if leftUnits[index] > rightUnits[index] {
			return 1
		}
	}
	if len(leftUnits) < len(rightUnits) {
		return -1
	}
	if len(leftUnits) > len(rightUnits) {
		return 1
	}
	return 0
}

// artifactMap indexes selected artifacts by kind and logical type using an unambiguous separator.
func artifactMap(manifest Manifest) map[string]Artifact {
	result := make(map[string]Artifact, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		result[string(item.Kind)+"\x00"+item.Type] = item
	}
	return result
}

// unionKeys returns the distinct logical artifact keys from both maps in canonical lexical order.
func unionKeys(left, right map[string]Artifact) []string {
	values := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	for key := range seen {
		values = append(values, key)
	}
	sort.Slice(values, func(i, j int) bool { return compareUTF16(values[i], values[j]) < 0 })
	return values
}

// annotationOnly proves both schema payloads differ only in allowed annotations and preserve comparison-relevant artifact relations.
func annotationOnly(before, after Artifact, context Context) bool {
	left, ok := context.Artifacts[Key{before.Kind, before.Type, before.Revision}]
	if !ok {
		return false
	}
	right, ok := context.Artifacts[Key{after.Kind, after.Type, after.Revision}]
	if !ok {
		return false
	}
	if !sameSchemaRelations(before, after) || left.Payload.MediaType != artifactmodel.SchemaMediaType || right.Payload.MediaType != artifactmodel.SchemaMediaType {
		return false
	}
	leftValue, err := strictjson.Parse(left.PayloadBytes)
	if err != nil {
		return false
	}
	rightValue, err := strictjson.Parse(right.PayloadBytes)
	if err != nil {
		return false
	}
	return equalWithoutAnnotations(leftValue, rightValue)
}

// sameSchemaRelations compares schema metadata after excluding revisions, creation releases, payloads, canonical identifiers, and additional resources.
func sameSchemaRelations(left, right Artifact) bool {
	left.Revision = ""
	right.Revision = ""
	left.CreatedUnderSchemaVersion = ""
	right.CreatedUnderSchemaVersion = ""
	left.Payload = artifactmodel.Descriptor{}
	right.Payload = artifactmodel.Descriptor{}
	left.CanonicalURI = ""
	right.CanonicalURI = ""
	left.Resources = nil
	right.Resources = nil
	return reflect.DeepEqual(left, right)
}

// equalWithoutAnnotations recursively compares JSON values while ignoring title, description, $comment, and examples object members.
func equalWithoutAnnotations(left, right any) bool {
	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if leftOK != rightOK {
		return false
	}
	if leftOK {
		for key, leftValue := range leftMap {
			if key == "title" || key == "description" || key == "$comment" || key == "examples" {
				continue
			}
			rightValue, ok := rightMap[key]
			if !ok || !equalWithoutAnnotations(leftValue, rightValue) {
				return false
			}
		}
		for key := range rightMap {
			if key == "title" || key == "description" || key == "$comment" || key == "examples" {
				continue
			}
			if _, ok := leftMap[key]; !ok {
				return false
			}
		}
		return true
	}
	leftArray, leftOK := left.([]any)
	rightArray, rightOK := right.([]any)
	if leftOK != rightOK {
		return false
	}
	if leftOK {
		if len(leftArray) != len(rightArray) {
			return false
		}
		for index := range leftArray {
			if !equalWithoutAnnotations(leftArray[index], rightArray[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

// diffBindings expands changed schema-context pairs into additive additions and breaking removals.
func diffBindings(before, after Manifest) []BindingChange {
	left, right := bindingPairs(before), bindingPairs(after)
	result := make([]BindingChange, 0)
	for key := range left {
		if _, ok := right[key]; !ok {
			result = append(result, BindingChange{Operation: OperationRemove, SchemaRevision: left[key][0], ContextRevision: left[key][1], Impact: ImpactBreaking})
		}
	}
	for key := range right {
		if _, ok := left[key]; !ok {
			result = append(result, BindingChange{Operation: OperationAdd, SchemaRevision: right[key][0], ContextRevision: right[key][1], Impact: ImpactAdditive})
		}
	}
	return result
}

// bindingPairs indexes each exact schema-context pair independently of its enclosing binding entry.
func bindingPairs(manifest Manifest) map[string][2]string {
	result := map[string][2]string{}
	for _, binding := range manifest.SchemaContextBindings {
		for _, context := range binding.ContextRevisions {
			result[binding.SchemaRevision+"\x00"+context] = [2]string{binding.SchemaRevision, context}
		}
	}
	return result
}

// diffURIs identifies catalog additions, removals, and changed artifact or resource targets without following intermediate releases.
func diffURIs(before, after Manifest) []URIChange {
	left, right := map[string]URIEntry{}, map[string]URIEntry{}
	for _, entry := range before.URICatalog {
		left[entry.URI] = entry
	}
	for _, entry := range after.URICatalog {
		right[entry.URI] = entry
	}
	result := make([]URIChange, 0)
	for uri, entry := range left {
		current, ok := right[uri]
		if !ok {
			result = append(result, URIChange{Operation: OperationRemove, URI: uri, BeforeRevision: entry.ArtifactRevision, BeforeResource: entry.Resource, Impact: ImpactBreaking})
		} else if current.ArtifactRevision != entry.ArtifactRevision || current.Resource != entry.Resource {
			result = append(result, URIChange{Operation: OperationUpdate, URI: uri, BeforeRevision: entry.ArtifactRevision, BeforeResource: entry.Resource, AfterRevision: current.ArtifactRevision, AfterResource: current.Resource, Impact: ImpactBreaking})
		}
	}
	for uri, entry := range right {
		if _, ok := left[uri]; !ok {
			result = append(result, URIChange{Operation: OperationAdd, URI: uri, AfterRevision: entry.ArtifactRevision, AfterResource: entry.Resource, Impact: ImpactAdditive})
		}
	}
	return result
}

// diffStorage expands changed schema-store pairs into additive additions and breaking removals.
func diffStorage(before, after Manifest) []StorageChange {
	left, right := storagePairs(before), storagePairs(after)
	result := make([]StorageChange, 0)
	for key, item := range left {
		if _, ok := right[key]; !ok {
			result = append(result, StorageChange{Operation: OperationRemove, Kind: item.Kind, ArtifactType: item.ArtifactType, Store: item.Store, Impact: ImpactBreaking})
		}
	}
	for key, item := range right {
		if _, ok := left[key]; !ok {
			result = append(result, StorageChange{Operation: OperationAdd, Kind: item.Kind, ArtifactType: item.ArtifactType, Store: item.Store, Impact: ImpactAdditive})
		}
	}
	return result
}

// storagePairs indexes each logical artifact and store association independently of its enclosing storage entry.
func storagePairs(manifest Manifest) map[string]StorageChange {
	result := map[string]StorageChange{}
	for _, entry := range manifest.StorageCatalog {
		for _, store := range entry.Stores {
			item := StorageChange{Kind: entry.Kind, ArtifactType: entry.ArtifactType, Store: store}
			result[string(item.Kind)+"\x00"+item.ArtifactType+"\x00"+string(store)] = item
		}
	}
	return result
}

// classify selects breaking before additive, defaulting to operational when neither impact occurs.
func classify(changes Changes) Compatibility {
	breaking, additive := false, false
	for _, change := range changes.Artifacts {
		breaking = breaking || change.Impact == ImpactBreaking
		additive = additive || change.Impact == ImpactAdditive
	}
	for _, change := range changes.SchemaContextBindings {
		breaking = breaking || change.Impact == ImpactBreaking
		additive = additive || change.Impact == ImpactAdditive
	}
	for _, change := range changes.URICatalog {
		breaking = breaking || change.Impact == ImpactBreaking
		additive = additive || change.Impact == ImpactAdditive
	}
	for _, change := range changes.StorageCatalog {
		breaking = breaking || change.Impact == ImpactBreaking
		additive = additive || change.Impact == ImpactAdditive
	}
	if breaking {
		return CompatibilityBreaking
	}
	if additive {
		return CompatibilityAdditive
	}
	return CompatibilityOperational
}

// sortChanges orders all change collections in place using the manifest-defined lexical sort keys.
func sortChanges(changes *Changes) {
	sort.Slice(changes.Artifacts, func(i, j int) bool {
		return compareMany(string(changes.Artifacts[i].Kind), changes.Artifacts[i].ArtifactType, string(changes.Artifacts[j].Kind), changes.Artifacts[j].ArtifactType) < 0 || compareMany(string(changes.Artifacts[i].Kind), changes.Artifacts[i].ArtifactType, string(changes.Artifacts[j].Kind), changes.Artifacts[j].ArtifactType) == 0 && compareUTF16(changes.Artifacts[i].Operation, changes.Artifacts[j].Operation) < 0
	})
	sort.Slice(changes.SchemaContextBindings, func(i, j int) bool {
		return compareMany(changes.SchemaContextBindings[i].SchemaRevision, changes.SchemaContextBindings[i].ContextRevision, changes.SchemaContextBindings[j].SchemaRevision, changes.SchemaContextBindings[j].ContextRevision) < 0 || compareMany(changes.SchemaContextBindings[i].SchemaRevision, changes.SchemaContextBindings[i].ContextRevision, changes.SchemaContextBindings[j].SchemaRevision, changes.SchemaContextBindings[j].ContextRevision) == 0 && compareUTF16(changes.SchemaContextBindings[i].Operation, changes.SchemaContextBindings[j].Operation) < 0
	})
	sort.Slice(changes.URICatalog, func(i, j int) bool {
		if c := compareUTF16(changes.URICatalog[i].URI, changes.URICatalog[j].URI); c != 0 {
			return c < 0
		}
		return compareUTF16(changes.URICatalog[i].Operation, changes.URICatalog[j].Operation) < 0
	})
	sort.Slice(changes.StorageCatalog, func(i, j int) bool {
		if c := compareMany(string(changes.StorageCatalog[i].Kind), changes.StorageCatalog[i].ArtifactType, string(changes.StorageCatalog[j].Kind), changes.StorageCatalog[j].ArtifactType); c != 0 {
			return c < 0
		}
		if c := compareUTF16(string(changes.StorageCatalog[i].Store), string(changes.StorageCatalog[j].Store)); c != 0 {
			return c < 0
		}
		return compareUTF16(changes.StorageCatalog[i].Operation, changes.StorageCatalog[j].Operation) < 0
	})
}

// equalChanges deeply compares machine-readable changes while excluding optional human-readable summaries.
func equalChanges(left, right Changes) bool {
	left.Summary = ""
	right.Summary = ""
	return reflect.DeepEqual(left, right)
}

// validIncrement checks the exact next major, minor, or patch release required by the compatibility classification.
func validIncrement(before, after string, compatibility Compatibility) bool {
	major, minor, patch, ok := versionParts(before)
	if !ok {
		return false
	}
	expected := ""
	switch compatibility {
	case CompatibilityBreaking:
		expected = strconv.Itoa(major+1) + ".0.0"
	case CompatibilityAdditive:
		expected = strconv.Itoa(major) + "." + strconv.Itoa(minor+1) + ".0"
	case CompatibilityOperational:
		expected = strconv.Itoa(major) + "." + strconv.Itoa(minor) + "." + strconv.Itoa(patch+1)
	default:
		return false
	}
	return after == expected
}

// versionParts splits three decimal components into machine-sized integers; callers enforce semantic-version lexical rules separately.
func versionParts(value string) (int, int, int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	result := [3]int{}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return 0, 0, 0, false
		}
		result[index] = number
	}
	return result[0], result[1], result[2], true
}
