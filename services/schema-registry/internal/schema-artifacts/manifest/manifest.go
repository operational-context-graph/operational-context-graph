// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
)

const (
	MediaType            = "application/vnd.apeiro.ocg.schema-release-manifest.v1+json"
	FormatVersion        = "1.0.0"
	MaxManifestBytes     = strictjson.MaxBytes
	MaxCollectionEntries = strictjson.MaxArrayEntries
)

var (
	// ErrInvalid classifies malformed or semantically invalid manifest input.
	ErrInvalid       = errors.New("invalid release manifest")
	errInvalid       = ErrInvalid
	commitPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	extensionPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
)

// Manifest is the complete selected artifact closure for one schema version.
// Raw contains the submitted authoritative JCS bytes after Parse succeeds.
type Manifest struct {
	Schema                string
	FormatVersion         string
	SchemaVersion         string
	PreviousRelease       *PreviousRelease
	Source                Source
	Artifacts             []Artifact
	SchemaContextBindings []Binding
	URICatalog            []URIEntry
	StorageCatalog        []StorageEntry
	Changes               *Changes
	Extensions            map[string]any
	Raw                   []byte
}

type PreviousRelease struct {
	SchemaVersion  string
	ManifestDigest string
}

type Source struct{ Repository, Commit string }

// Artifact is a release selection. Bytes are supplied through Context, never JSON.
type Artifact struct {
	Kind                      artifact.Kind
	Type                      string
	Revision                  string
	CreatedUnderSchemaVersion string
	SchemaClass               artifact.SchemaClass
	TargetSchema              string
	CanonicalURI              string
	Payload                   artifact.Descriptor
	Resources                 []artifact.Resource
	Dependencies              []artifact.Dependency
	Extensions                map[string]any
}

type Binding struct {
	SchemaRevision   string
	ContextRevisions []string
}
type URIEntry struct{ URI, ArtifactRevision, Resource string }
type StorageEntry struct {
	Kind         artifact.Kind
	ArtifactType string
	Stores       []string
}

type Changes struct {
	Summary               string
	Compatibility         string
	Artifacts             []ArtifactChange
	SchemaContextBindings []BindingChange
	URICatalog            []URIChange
	StorageCatalog        []StorageChange
}
type ArtifactChange struct {
	Operation                                           string
	Kind                                                artifact.Kind
	ArtifactType, BeforeRevision, AfterRevision, Impact string
}
type BindingChange struct{ Operation, SchemaRevision, ContextRevision, Impact string }
type URIChange struct{ Operation, URI, BeforeRevision, BeforeResource, AfterRevision, AfterResource, Impact string }
type StorageChange struct {
	Operation                   string
	Kind                        artifact.Kind
	ArtifactType, Store, Impact string
}

// Key identifies one immutable artifact revision.
type Key struct {
	Kind           artifact.Kind
	Type, Revision string
}

// Release records a previously validated manifest and its digest.
type Release struct {
	Manifest Manifest
	Digest   string
}

// Context provides caller-owned immutable bytes and predecessor lineage.
// Artifacts MUST contain exact payload and resource bytes for every selection.
type Context struct {
	RegistryOrigin string
	Artifacts      map[Key]artifact.Artifact
	History        []Release // oldest first; the immediate predecessor is the final item.
}

// Parse strictly decodes a version 1 manifest and requires submitted bytes to be JCS.
func Parse(data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
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
	if err := strictjson.RejectUnknownFields(value, "$schema", "formatVersion", "schemaVersion", "previousRelease", "source", "artifacts", "schemaContextBindings", "uriCatalog", "storageCatalog", "changes", "extensions"); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest fields: %v", errInvalid, err)
	}
	m := Manifest{Raw: append([]byte(nil), data...)}
	if m.Schema, err = requiredString(value, "$schema"); err != nil {
		return Manifest{}, err
	}
	if m.FormatVersion, err = requiredString(value, "formatVersion"); err != nil {
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

// Validate validates a parsed immutable manifest without altering it.
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
	selected := make(map[Key]artifact.Artifact, len(m.Artifacts))
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
		if err := bound.Validate(context.RegistryOrigin); err != nil {
			return fmt.Errorf("%w: artifact %s: %v", errInvalid, key.Type, err)
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
	selectedByRevision := make(map[string]artifact.Artifact, len(selected))
	for _, bound := range selected {
		selectedByRevision[bound.Revision] = bound
	}
	for _, entry := range m.URICatalog {
		bound, ok := selectedByRevision[entry.ArtifactRevision]
		if !ok {
			continue
		}
		if entry.Resource == "" {
			value, err := strictjson.Parse(bound.PayloadBytes)
			if err != nil {
				return fmt.Errorf("%w: catalog payload JSON: %v", errInvalid, err)
			}
			formatCatalog[entry.URI] = value
		}
	}
	for _, bound := range selected {
		if err := bound.ValidatePayloadFormatWithCatalog(formatCatalog); err != nil {
			return fmt.Errorf("%w: validate artifact format: %v", errInvalid, err)
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

func (m Manifest) validateStructural() error {
	parsed, err := url.Parse(m.Schema)
	if err != nil || !parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" || m.FormatVersion != FormatVersion || !artifact.ValidVersion(m.SchemaVersion) {
		return fmt.Errorf("%w: invalid format declaration", errInvalid)
	}
	if !commitPattern.MatchString(m.Source.Commit) || !absoluteURI(m.Source.Repository) {
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
		if !absoluteURI(entry.URI) || !artifact.ValidRevision(entry.ArtifactRevision) || (entry.Resource != "" && !artifact.ValidResourceName(entry.Resource)) {
			return fmt.Errorf("%w: invalid URI catalog entry", errInvalid)
		}
	}
	for _, entry := range m.StorageCatalog {
		if entry.Kind != artifact.KindSchema || !artifact.ValidLogicalType(entry.ArtifactType) || len(entry.Stores) == 0 || checkCount(len(entry.Stores)) != nil {
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

func (m Manifest) validateReferences(selected map[Key]artifact.Artifact, revisions map[string]Artifact) error {
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
		if !ok || schema.Kind != artifact.KindSchema {
			return fmt.Errorf("%w: binding schema is unresolved", errInvalid)
		}
		if _, exists := bindings[binding.SchemaRevision]; exists {
			return fmt.Errorf("%w: duplicate schema binding", errInvalid)
		}
		bindings[binding.SchemaRevision] = struct{}{}
		contexts := make(map[string]struct{}, len(binding.ContextRevisions))
		for _, revision := range binding.ContextRevisions {
			context, ok := revisions[revision]
			if !ok || context.Kind != artifact.KindContext {
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
		if !ok || selectedSchema.SchemaClass == artifact.SchemaClassSource {
			return fmt.Errorf("%w: invalid schema storage placement", errInvalid)
		}
		key := string(entry.Kind) + "\x00" + entry.ArtifactType
		if _, exists := placements[key]; exists {
			return fmt.Errorf("%w: duplicate storage placement", errInvalid)
		}
		placements[key] = struct{}{}
	}
	for _, item := range m.Artifacts {
		if item.Kind != artifact.KindSchema {
			continue
		}
		_, placed := placements[string(item.Kind)+"\x00"+item.Type]
		if (item.SchemaClass == artifact.SchemaClassSource) == placed {
			return fmt.Errorf("%w: schema storage placement does not agree with class", errInvalid)
		}
	}
	return nil
}

func (m Manifest) validateSourceMappings(selected map[Key]artifact.Artifact, context Context) error {
	schemas := make([]sourcemapping.Schema, 0)
	for _, item := range m.Artifacts {
		if item.Kind == artifact.KindSchema {
			schemas = append(schemas, sourcemapping.Schema{Type: item.Type, Revision: item.Revision, Class: item.SchemaClass})
		}
	}
	lineage := []sourcemapping.Release{{Version: m.SchemaVersion, Schemas: schemas}}
	for _, v := range slices.Backward(context.History) {
		prior := v.Manifest
		priorSchemas := make([]sourcemapping.Schema, 0)
		for _, item := range prior.Artifacts {
			if item.Kind == artifact.KindSchema {
				priorSchemas = append(priorSchemas, sourcemapping.Schema{Type: item.Type, Revision: item.Revision, Class: item.SchemaClass})
			}
		}
		lineage = append(lineage, sourcemapping.Release{Version: prior.SchemaVersion, Schemas: priorSchemas})
	}
	mappings := make([]artifact.Artifact, 0)
	for _, item := range m.Artifacts {
		if item.Kind == artifact.KindSourceMapping {
			mappings = append(mappings, selected[Key{item.Kind, item.Type, item.Revision}])
		}
	}
	_, err := sourcemapping.ValidateRelease(mappings, sourcemapping.ValidationContext{Version: m.SchemaVersion, Lineage: lineage})
	if err != nil {
		return fmt.Errorf("%w: source mapping agreement: %v", errInvalid, err)
	}
	return nil
}

func (m Manifest) validateChanges(context Context) error {
	if m.Changes == nil {
		if len(context.History) > 0 {
			diff, err := Diff(context.History[len(context.History)-1].Manifest, m, context)
			if err != nil {
				return err
			}
			if diff.Compatibility == "operational" && len(diff.Artifacts) == 0 && len(diff.SchemaContextBindings) == 0 && len(diff.URICatalog) == 0 && len(diff.StorageCatalog) == 0 {
				return fmt.Errorf("%w: release has no effective difference", errInvalid)
			}
		}
		return nil
	}
	if len(context.History) == 0 {
		if m.Changes.Compatibility != "initial" || len(m.Changes.Artifacts)+len(m.Changes.SchemaContextBindings)+len(m.Changes.URICatalog)+len(m.Changes.StorageCatalog) != 0 {
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
func Diff(before, after Manifest, context Context) (Changes, error) {
	changes := Changes{}
	beforeArtifacts, afterArtifacts := artifactMap(before), artifactMap(after)
	keys := unionKeys(beforeArtifacts, afterArtifacts)
	for _, key := range keys {
		left, hasLeft := beforeArtifacts[key]
		right, hasRight := afterArtifacts[key]
		switch {
		case !hasLeft:
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: "add", Kind: right.Kind, ArtifactType: right.Type, AfterRevision: right.Revision, Impact: "additive"})
		case !hasRight:
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: "remove", Kind: left.Kind, ArtifactType: left.Type, BeforeRevision: left.Revision, Impact: "breaking"})
		case left.Revision != right.Revision:
			impact := "breaking"
			if left.Kind == artifact.KindSchema && annotationOnly(left, right, context) {
				impact = "operational"
			}
			changes.Artifacts = append(changes.Artifacts, ArtifactChange{Operation: "update", Kind: right.Kind, ArtifactType: right.Type, BeforeRevision: left.Revision, AfterRevision: right.Revision, Impact: impact})
		}
	}
	changes.SchemaContextBindings = diffBindings(before, after)
	changes.URICatalog = diffURIs(before, after)
	changes.StorageCatalog = diffStorage(before, after)
	changes.Compatibility = classify(changes)
	if changes.Compatibility == "operational" && len(changes.Artifacts)+len(changes.SchemaContextBindings)+len(changes.URICatalog)+len(changes.StorageCatalog) == 0 {
		changes.Compatibility = "operational"
	}
	sortChanges(&changes)
	return changes, nil
}

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
	if !artifact.ValidVersion(version) || !artifact.ValidDigest(digest) {
		return nil, invalid("invalid previous release")
	}
	return &PreviousRelease{version, digest}, nil
}
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
		if err := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "schemaClass", "targetSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "extensions"); err != nil {
			return nil, err
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
func parseDescriptor(raw any) (artifact.Descriptor, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return artifact.Descriptor{}, invalid("descriptor must be object")
	}
	if err := strictjson.RejectUnknownFields(object, "mediaType", "digest", "size", "bundlePath"); err != nil {
		return artifact.Descriptor{}, err
	}
	media, err := requiredString(object, "mediaType")
	if err != nil {
		return artifact.Descriptor{}, err
	}
	digest, err := requiredString(object, "digest")
	if err != nil {
		return artifact.Descriptor{}, err
	}
	path, err := requiredString(object, "bundlePath")
	if err != nil {
		return artifact.Descriptor{}, err
	}
	size, err := number(object, "size")
	if err != nil {
		return artifact.Descriptor{}, err
	}
	return artifact.Descriptor{MediaType: media, Digest: digest, Size: size, BundlePath: path}, nil
}
func parseResources(raw any) ([]artifact.Resource, error) {
	values, err := array(raw, "resources")
	if err != nil {
		return nil, err
	}
	result := make([]artifact.Resource, 0, len(values))
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
		result = append(result, artifact.Resource{Name: name, Role: role, Descriptor: descriptor})
	}
	return result, nil
}

func parseResourceDescriptor(object map[string]any) (artifact.Descriptor, error) {
	return parseDescriptor(map[string]any{
		"mediaType":  object["mediaType"],
		"digest":     object["digest"],
		"size":       object["size"],
		"bundlePath": object["bundlePath"],
	})
}
func parseDependencies(raw any) ([]artifact.Dependency, error) {
	values, err := array(raw, "dependencies")
	if err != nil {
		return nil, err
	}
	result := make([]artifact.Dependency, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("dependency must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision"); err != nil {
			return nil, err
		}
		item := artifact.Dependency{}
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
		if err := strictjson.RejectUnknownFields(object, "kind", "artifactType", "stores"); err != nil {
			return nil, err
		}
		item := StorageEntry{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Stores, err = stringsArray(object["stores"], "stores"); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

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
	if changes.Compatibility, err = requiredString(object, "compatibility"); err != nil {
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
	if changes.Compatibility != "initial" && changes.Compatibility != "breaking" && changes.Compatibility != "additive" && changes.Compatibility != "operational" {
		return nil, invalid("invalid changes compatibility")
	}
	return changes, nil
}

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
		if err := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "beforeRevision", "afterRevision", "impact"); err != nil {
			return nil, err
		}
		item := ArtifactChange{}
		if item.Operation, err = requiredString(object, "operation"); err != nil {
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
		if item.Impact, err = requiredString(object, "impact"); err != nil {
			return nil, err
		}
		if err := validateArtifactChange(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

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
		if err := strictjson.RejectUnknownFields(object, "operation", "schemaRevision", "contextRevision", "impact"); err != nil {
			return nil, err
		}
		item := BindingChange{}
		if item.Operation, err = requiredString(object, "operation"); err != nil {
			return nil, err
		}
		if item.SchemaRevision, err = requiredString(object, "schemaRevision"); err != nil {
			return nil, err
		}
		if item.ContextRevision, err = requiredString(object, "contextRevision"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredString(object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != "add" && item.Operation != "remove") || !validImpact(item.Impact) {
			return nil, invalid("invalid binding change")
		}
		result = append(result, item)
	}
	return result, nil
}

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
		if err := strictjson.RejectUnknownFields(object, "operation", "uri", "beforeRevision", "beforeResource", "afterRevision", "afterResource", "impact"); err != nil {
			return nil, err
		}
		item := URIChange{}
		if item.Operation, err = requiredString(object, "operation"); err != nil {
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
		if item.Impact, err = requiredString(object, "impact"); err != nil {
			return nil, err
		}
		if !absoluteURI(item.URI) || !validChangeShape(item.Operation, item.BeforeRevision, item.AfterRevision) || !validImpact(item.Impact) || (item.BeforeResource != "" && !artifact.ValidResourceName(item.BeforeResource)) || (item.AfterResource != "" && !artifact.ValidResourceName(item.AfterResource)) {
			return nil, invalid("invalid URI change")
		}
		result = append(result, item)
	}
	return result, nil
}

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
		if err := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "store", "impact"); err != nil {
			return nil, err
		}
		item := StorageChange{}
		if item.Operation, err = requiredString(object, "operation"); err != nil {
			return nil, err
		}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Store, err = requiredString(object, "store"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredString(object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != "add" && item.Operation != "remove") || !validStore(item.Store) || !validImpact(item.Impact) {
			return nil, invalid("invalid storage change")
		}
		result = append(result, item)
	}
	return result, nil
}

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
func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return value, nil
}
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
func kind(object map[string]any, name string) (artifact.Kind, error) {
	value, err := requiredString(object, name)
	if err != nil {
		return "", err
	}
	kind := artifact.Kind(value)
	if kind != artifact.KindSchema && kind != artifact.KindContext && kind != artifact.KindSourceMapping {
		return "", invalid("invalid artifact kind")
	}
	return kind, nil
}
func optionalSchemaClass(object map[string]any, name string) (artifact.SchemaClass, error) {
	value, err := optionalString(object, name)
	if err != nil {
		return "", err
	}
	return artifact.SchemaClass(value), nil
}
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
func invalid(message string) error { return fmt.Errorf("%w: %s", errInvalid, message) }
func checkCount(count int) error {
	if count > MaxCollectionEntries {
		return invalid("collection exceeds entry limit")
	}
	return nil
}
func absoluteURI(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.RawQuery == "" && parsed.Fragment == ""
}
func validStore(value string) bool {
	switch value {
	case "data-center-inventory", "telemetry-store", "insights-repository", "recorded-actions-repository":
		return true
	}
	return false
}
func validImpact(value string) bool {
	return value == "breaking" || value == "additive" || value == "operational"
}
func validateArtifact(item Artifact) error {
	if !artifact.ValidLogicalType(item.Type) || !artifact.ValidRevision(item.Revision) || !artifact.ValidVersion(item.CreatedUnderSchemaVersion) || !absoluteURI(item.CanonicalURI) {
		return invalid("invalid artifact identity")
	}
	if !artifact.ValidDigest(item.Payload.Digest) || item.Payload.Size < 0 || !artifact.ValidBundlePath(item.Payload.BundlePath) {
		return invalid("invalid payload descriptor")
	}
	if item.Kind == artifact.KindSchema {
		if item.SchemaClass != artifact.SchemaClassEntity && item.SchemaClass != artifact.SchemaClassIdentifier && item.SchemaClass != artifact.SchemaClassEdge && item.SchemaClass != artifact.SchemaClassSource || item.TargetSchema != "" {
			return invalid("invalid schema fields")
		}
	} else if item.Kind == artifact.KindContext {
		if item.SchemaClass != "" || item.TargetSchema != "" {
			return invalid("invalid context fields")
		}
	} else if item.SchemaClass != "" || !artifact.ValidVersion(item.TargetSchema) {
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
func validateArtifactChange(item ArtifactChange) error {
	if !artifact.ValidLogicalType(item.ArtifactType) || !validImpact(item.Impact) || !validChangeShape(item.Operation, item.BeforeRevision, item.AfterRevision) {
		return invalid("invalid artifact change")
	}
	if item.Operation == "remove" && item.Impact != "breaking" {
		return invalid("artifact removal must be breaking")
	}
	return nil
}
func validChangeShape(operation, before, after string) bool {
	switch operation {
	case "add":
		return before == "" && artifact.ValidRevision(after)
	case "update":
		return artifact.ValidRevision(before) && artifact.ValidRevision(after) && before != after
	case "remove":
		return artifact.ValidRevision(before) && after == ""
	}
	return false
}

func matchesSelection(selection Artifact, bound artifact.Artifact) bool {
	if selection.Kind != bound.Kind || selection.Type != bound.Type || selection.Revision != bound.Revision || selection.CreatedUnderSchemaVersion != bound.CreatedUnder || selection.SchemaClass != bound.SchemaClass || selection.TargetSchema != bound.TargetSchema || selection.CanonicalURI != bound.CanonicalURI || !reflect.DeepEqual(selection.Payload, bound.Payload) || !sameResources(selection.Resources, stripResourceBytes(bound.Resources)) || !sameDependencies(selection.Dependencies, bound.Dependencies) || !sameExtensions(selection.Extensions, bound.Extensions) {
		return false
	}
	return true
}
func sameResources(left, right []artifact.Resource) bool {
	return len(left) == len(right) && reflect.DeepEqual(left, right)
}
func sameDependencies(left, right []artifact.Dependency) bool {
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
func sameExtensions(left, right map[string]any) bool {
	return len(left) == len(right) && reflect.DeepEqual(left, right)
}
func stripResourceBytes(resources []artifact.Resource) []artifact.Resource {
	result := make([]artifact.Resource, len(resources))
	copy(result, resources)
	for index := range result {
		result[index].Bytes = nil
	}
	return result
}
func immutableReuse(selection Artifact, history []Release) error {
	for _, release := range history {
		for _, previous := range release.Manifest.Artifacts {
			if previous.Kind == selection.Kind && previous.Type == selection.Type && previous.Revision == selection.Revision && !reflect.DeepEqual(previous, selection) {
				return fmt.Errorf("%w: immutable revision changed", errInvalid)
			}
		}
	}
	return nil
}
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
func findSelected(items []Artifact, kind artifact.Kind, artifactType string) (Artifact, bool) {
	for _, item := range items {
		if item.Kind == kind && item.Type == artifactType {
			return item, true
		}
	}
	return Artifact{}, false
}
func hasResource(item Artifact, name string) bool {
	for _, resource := range item.Resources {
		if resource.Name == name {
			return true
		}
	}
	return false
}
func findURIEntry(entries []URIEntry, uri string) (URIEntry, bool) {
	for _, entry := range entries {
		if entry.URI == uri {
			return entry, true
		}
	}
	return URIEntry{}, false
}

func requiredURIs(item artifact.Artifact) []string {
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

func (m Manifest) validateOrder() error {
	if !sorted(m.Artifacts, func(left, right Artifact) int {
		return compareMany(string(left.Kind), left.Type, string(right.Kind), right.Type)
	}) {
		return invalid("artifacts are not ordered")
	}
	for _, item := range m.Artifacts {
		if !sorted(item.Resources, func(left, right artifact.Resource) int {
			return compareMany(left.Role, left.Name, right.Role, right.Name)
		}) || !sorted(item.Dependencies, func(left, right artifact.Dependency) int {
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
		if !sorted(entry.Stores, compareUTF16) {
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

func sorted[T any](values []T, compare func(T, T) int) bool {
	for index := 1; index < len(values); index++ {
		if compare(values[index-1], values[index]) >= 0 {
			return false
		}
	}
	return true
}
func compareMany(values ...string) int {
	if len(values)%2 != 0 {
		panic("compareMany requires left and right fields")
	}
	half := len(values) / 2
	for index := 0; index < half; index++ {
		if comparison := compareUTF16(values[index], values[index+half]); comparison != 0 {
			return comparison
		}
	}
	return 0
}
func compareUTF16(left, right string) int {
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

func artifactMap(manifest Manifest) map[string]Artifact {
	result := make(map[string]Artifact, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		result[string(item.Kind)+"\x00"+item.Type] = item
	}
	return result
}
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
func annotationOnly(before, after Artifact, context Context) bool {
	left, ok := context.Artifacts[Key{before.Kind, before.Type, before.Revision}]
	if !ok {
		return false
	}
	right, ok := context.Artifacts[Key{after.Kind, after.Type, after.Revision}]
	if !ok {
		return false
	}
	if !sameSchemaRelations(before, after) || left.Payload.MediaType != artifact.SchemaMediaType || right.Payload.MediaType != artifact.SchemaMediaType {
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
func sameSchemaRelations(left, right Artifact) bool {
	left.Revision = ""
	right.Revision = ""
	left.CreatedUnderSchemaVersion = ""
	right.CreatedUnderSchemaVersion = ""
	left.Payload = artifact.Descriptor{}
	right.Payload = artifact.Descriptor{}
	left.CanonicalURI = ""
	right.CanonicalURI = ""
	left.Resources = nil
	right.Resources = nil
	return reflect.DeepEqual(left, right)
}
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

func diffBindings(before, after Manifest) []BindingChange {
	left, right := bindingPairs(before), bindingPairs(after)
	result := make([]BindingChange, 0)
	for key := range left {
		if _, ok := right[key]; !ok {
			result = append(result, BindingChange{Operation: "remove", SchemaRevision: left[key][0], ContextRevision: left[key][1], Impact: "breaking"})
		}
	}
	for key := range right {
		if _, ok := left[key]; !ok {
			result = append(result, BindingChange{Operation: "add", SchemaRevision: right[key][0], ContextRevision: right[key][1], Impact: "additive"})
		}
	}
	return result
}
func bindingPairs(manifest Manifest) map[string][2]string {
	result := map[string][2]string{}
	for _, binding := range manifest.SchemaContextBindings {
		for _, context := range binding.ContextRevisions {
			result[binding.SchemaRevision+"\x00"+context] = [2]string{binding.SchemaRevision, context}
		}
	}
	return result
}
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
			result = append(result, URIChange{Operation: "remove", URI: uri, BeforeRevision: entry.ArtifactRevision, BeforeResource: entry.Resource, Impact: "breaking"})
		} else if current.ArtifactRevision != entry.ArtifactRevision || current.Resource != entry.Resource {
			result = append(result, URIChange{Operation: "update", URI: uri, BeforeRevision: entry.ArtifactRevision, BeforeResource: entry.Resource, AfterRevision: current.ArtifactRevision, AfterResource: current.Resource, Impact: "breaking"})
		}
	}
	for uri, entry := range right {
		if _, ok := left[uri]; !ok {
			result = append(result, URIChange{Operation: "add", URI: uri, AfterRevision: entry.ArtifactRevision, AfterResource: entry.Resource, Impact: "additive"})
		}
	}
	return result
}
func diffStorage(before, after Manifest) []StorageChange {
	left, right := storagePairs(before), storagePairs(after)
	result := make([]StorageChange, 0)
	for key, item := range left {
		if _, ok := right[key]; !ok {
			result = append(result, StorageChange{Operation: "remove", Kind: item.Kind, ArtifactType: item.ArtifactType, Store: item.Store, Impact: "breaking"})
		}
	}
	for key, item := range right {
		if _, ok := left[key]; !ok {
			result = append(result, StorageChange{Operation: "add", Kind: item.Kind, ArtifactType: item.ArtifactType, Store: item.Store, Impact: "additive"})
		}
	}
	return result
}
func storagePairs(manifest Manifest) map[string]StorageChange {
	result := map[string]StorageChange{}
	for _, entry := range manifest.StorageCatalog {
		for _, store := range entry.Stores {
			item := StorageChange{Kind: entry.Kind, ArtifactType: entry.ArtifactType, Store: store}
			result[string(item.Kind)+"\x00"+item.ArtifactType+"\x00"+store] = item
		}
	}
	return result
}
func classify(changes Changes) string {
	breaking, additive := false, false
	for _, change := range changes.Artifacts {
		breaking = breaking || change.Impact == "breaking"
		additive = additive || change.Impact == "additive"
	}
	for _, change := range changes.SchemaContextBindings {
		breaking = breaking || change.Impact == "breaking"
		additive = additive || change.Impact == "additive"
	}
	for _, change := range changes.URICatalog {
		breaking = breaking || change.Impact == "breaking"
		additive = additive || change.Impact == "additive"
	}
	for _, change := range changes.StorageCatalog {
		breaking = breaking || change.Impact == "breaking"
		additive = additive || change.Impact == "additive"
	}
	if breaking {
		return "breaking"
	}
	if additive {
		return "additive"
	}
	return "operational"
}
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
		if c := compareUTF16(changes.StorageCatalog[i].Store, changes.StorageCatalog[j].Store); c != 0 {
			return c < 0
		}
		return compareUTF16(changes.StorageCatalog[i].Operation, changes.StorageCatalog[j].Operation) < 0
	})
}
func equalChanges(left, right Changes) bool {
	left.Summary = ""
	right.Summary = ""
	return reflect.DeepEqual(left, right)
}
func validIncrement(before, after, compatibility string) bool {
	major, minor, patch, ok := versionParts(before)
	if !ok {
		return false
	}
	expected := ""
	switch compatibility {
	case "breaking":
		expected = strconv.Itoa(major+1) + ".0.0"
	case "additive":
		expected = strconv.Itoa(major) + "." + strconv.Itoa(minor+1) + ".0"
	case "operational":
		expected = strconv.Itoa(major) + "." + strconv.Itoa(minor) + "." + strconv.Itoa(patch+1)
	default:
		return false
	}
	return after == expected
}
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
