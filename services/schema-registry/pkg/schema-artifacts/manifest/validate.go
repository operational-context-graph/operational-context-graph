// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	sourcemappingmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// commitPattern accepts lowercase Git object identifiers using SHA-1 or SHA-256 lengths.
var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

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
	selected := make(map[artifactmodel.Dependency]artifactmodel.Artifact, len(m.Artifacts))
	byRevision := make(map[string]artifactmodel.Artifact, len(m.Artifacts))
	for _, selectedArtifact := range m.Artifacts {
		key := artifactmodel.Dependency{Kind: selectedArtifact.Kind, Type: selectedArtifact.Type, Revision: selectedArtifact.Revision}
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
func (m Manifest) validateReferences(selected map[artifactmodel.Dependency]artifactmodel.Artifact, revisions map[string]artifactmodel.Artifact) error {
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
			if _, ok := selected[dep]; !ok {
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
		bound := selected[artifactmodel.Dependency{Kind: item.Kind, Type: item.Type, Revision: item.Revision}]
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
func (m Manifest) validateSourceMappings(selected map[artifactmodel.Dependency]artifactmodel.Artifact, context Context) error {
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
			mappings = append(mappings, selected[artifactmodel.Dependency{Kind: item.Kind, Type: item.Type, Revision: item.Revision}])
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
func validateArtifact(item artifactmodel.Artifact) error {
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

// matchesSelection compares shared revision metadata without caller-supplied bytes.
func matchesSelection(selection, bound artifactmodel.Artifact) bool {
	if !slices.Equal(selection.Dependencies, bound.Dependencies) {
		return false
	}
	selection, bound = selection.Metadata(), bound.Metadata()
	selection.Dependencies, bound.Dependencies = nil, nil
	return reflect.DeepEqual(selection, bound)
}

// immutableReuse rejects changed revision metadata, reassigned canonical identifiers, and reuse of retired logical identities.
func immutableReuse(selection artifactmodel.Artifact, history []Release) error {
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
func revisionBoundSelection(value artifactmodel.Artifact) artifactmodel.Artifact {
	value = value.Metadata()
	value.Payload.BundlePath = ""
	for index := range value.Resources {
		value.Resources[index].BundlePath = ""
	}
	return value
}

// validateCreatedUnder preserves a reused revision's creation release and binds new revisions to the containing release.
func validateCreatedUnder(selection artifactmodel.Artifact, version string, history []Release) error {
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
func findSelected(items []artifactmodel.Artifact, kind artifactmodel.Kind, artifactType string) (artifactmodel.Artifact, bool) {
	for _, item := range items {
		if item.Kind == kind && item.Type == artifactType {
			return item, true
		}
	}
	return artifactmodel.Artifact{}, false
}

// hasResource reports whether the selection declares an owned resource with the exact requested name.
func hasResource(item artifactmodel.Artifact, name string) bool {
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
	if !sorted(m.Artifacts, func(left, right artifactmodel.Artifact) int {
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
		changes := m.Changes
		if !sorted(changes.Artifacts, func(a, b ArtifactChange) int {
			return compareMany(string(a.Kind), a.ArtifactType, string(a.Operation), string(b.Kind), b.ArtifactType, string(b.Operation))
		}) || !sorted(changes.SchemaContextBindings, func(a, b BindingChange) int {
			return compareMany(a.SchemaRevision, a.ContextRevision, string(a.Operation), b.SchemaRevision, b.ContextRevision, string(b.Operation))
		}) || !sorted(changes.URICatalog, func(a, b URIChange) int {
			return compareMany(a.URI, string(a.Operation), b.URI, string(b.Operation))
		}) || !sorted(changes.StorageCatalog, func(a, b StorageChange) int {
			return compareMany(string(a.Kind), a.ArtifactType, string(a.Store), string(a.Operation), string(b.Kind), b.ArtifactType, string(b.Store), string(b.Operation))
		}) {
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
