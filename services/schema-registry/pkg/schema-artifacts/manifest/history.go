package manifest

import (
	"fmt"
	"reflect"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// validateLineage checks the immediate predecessor binding and rejects repeated schema versions.
// History MUST contain trusted, previously validated releases in oldest-first order.
// An empty history requires an initial release with no predecessor. Violations wrap [ErrInvalid].
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

// historyIndex records immutable revisions, canonical URI ownership, and retired logical identities
// from validated release history.
type historyIndex struct {
	// revisions is keyed by global revision identifier, independent of artifact kind and logical type.
	revisions map[string]artifactmodel.Artifact
	canonical map[string]string
	// retired keys combine kind and logical type with a null-byte separator.
	retired map[string]bool
}

// indexHistory indexes trusted releases in oldest-first order without changing their metadata.
// A logical identity remains retired once it disappears after a prior selection.
func indexHistory(history []Release) historyIndex {
	index := historyIndex{revisions: make(map[string]artifactmodel.Artifact), canonical: make(map[string]string), retired: make(map[string]bool)}
	active := map[string]bool{}
	for _, release := range history {
		current := make(map[string]bool, len(release.Manifest.Artifacts))
		for _, a := range release.Manifest.Artifacts {
			current[string(a.Kind)+"\x00"+a.Type] = true
			index.revisions[a.Revision] = revisionBoundSelection(a)
			index.canonical[a.CanonicalURI] = a.Revision
		}
		for identity := range active {
			if !current[identity] {
				index.retired[identity] = true
			}
		}
		active = current
	}
	return index
}

// validateReuse rejects changed revision metadata, reassigned canonical URIs, and reuse of retired
// logical identities. It checks history, not candidate-local uniqueness or byte integrity.
// Violations wrap [ErrInvalid].
func (h historyIndex) validateReuse(selection artifactmodel.Artifact) error {
	if revision, exists := h.canonical[selection.CanonicalURI]; exists && revision != selection.Revision {
		return invalid("historical canonical URI is reassigned")
	}
	if previous, exists := h.revisions[selection.Revision]; exists && !reflect.DeepEqual(previous, revisionBoundSelection(selection)) {
		return invalid("immutable revision changed")
	}
	if h.retired[string(selection.Kind)+"\x00"+selection.Type] {
		return invalid("retired logical identity is reused")
	}
	return nil
}

// validateHistoricalURIs rejects changed content behind historical canonical URIs and schema keys.
// It compares media type, digest, and size. Changed JSON aliases require historical document-usage proof.
// Previously validated history and exact historical bytes MUST be supplied through context.
// Context-only aliases may select different revisions. Reference validation reports absent targets.
func (m Manifest) validateHistoricalURIs(context Context) error {
	currentByRevision := make(map[string]artifactmodel.Artifact, len(m.Artifacts))
	for _, a := range m.Artifacts {
		currentByRevision[a.Revision] = a
	}
	current := make(map[string]artifactmodel.Descriptor, len(m.URICatalog))
	for _, entry := range m.URICatalog {
		if a, exists := currentByRevision[entry.ArtifactRevision]; exists {
			if descriptor, ok := catalogDescriptor(a, entry.Resource); ok {
				current[entry.URI] = descriptor
			}
		}
	}
	reassigned := func(uri string, previous artifactmodel.Descriptor) bool {
		descriptor, exists := current[uri]
		return exists && (previous.Digest != descriptor.Digest || previous.Size != descriptor.Size || previous.MediaType != descriptor.MediaType)
	}
	for _, release := range context.History {
		byRevision := make(map[string]artifactmodel.Artifact, len(release.Manifest.Artifacts))
		for _, a := range release.Manifest.Artifacts {
			if reassigned(a.CanonicalURI, a.Payload) {
				return invalid("historical immutable URI is reassigned")
			}
			byRevision[a.Revision] = a
		}
		var schemas map[documentKey]bool
		for _, entry := range release.Manifest.URICatalog {
			a, exists := byRevision[entry.ArtifactRevision]
			if !exists {
				continue
			}
			descriptor, ok := catalogDescriptor(a, entry.Resource)
			if !ok || !reassigned(entry.URI, descriptor) {
				continue
			}
			if descriptor.MediaType == artifactmodel.SchemaMediaType {
				return invalid("historical immutable URI is reassigned")
			}
			if !jsonMediaType(descriptor.MediaType) {
				continue
			}
			if schemas == nil {
				var err error
				schemas, err = release.Manifest.historicalSchemaDocuments(context)
				if err != nil {
					return err
				}
			}
			if schemas[documentKey{entry.ArtifactRevision, entry.Resource}] {
				return invalid("historical immutable URI is reassigned")
			}
		}
	}
	return nil
}

// historicalSchemaDocuments discovers schema usage from a trusted release and supplied exact bytes.
// It checks historical byte integrity and metadata without revalidating lineage or change summaries.
// Missing or inconsistent historical bytes fail closed with errors wrapping [ErrInvalid].
func (m Manifest) historicalSchemaDocuments(context Context) (map[documentKey]bool, error) {
	selected := make(map[artifactmodel.Dependency]artifactmodel.Artifact, len(m.Artifacts))
	for _, a := range m.Artifacts {
		key := artifactmodel.Dependency{Kind: a.Kind, Type: a.Type, Revision: a.Revision}
		bound, exists := context.Artifacts[key]
		if !exists {
			return nil, invalid("historical document bytes are required to retarget JSON aliases")
		}
		if !matchesSelection(revisionBoundSelection(a), revisionBoundSelection(bound)) {
			return nil, invalid("historical artifact metadata differs from validated selection")
		}
		if err := bound.Validate(context.RegistryOrigin); err != nil {
			return nil, fmt.Errorf("%w: historical artifact bytes: %w", ErrInvalid, err)
		}
		selected[key] = bound
	}
	catalog, err := m.documentCatalog(selected)
	if err != nil {
		return nil, fmt.Errorf("%w: historical document usage: %w", ErrInvalid, err)
	}
	return catalog.schemas, nil
}

// catalogDescriptor returns the selected payload or named resource descriptor.
// An empty resource selects the payload. It returns false when a named resource is absent.
func catalogDescriptor(a artifactmodel.Artifact, resource string) (artifactmodel.Descriptor, bool) {
	if resource == "" {
		return a.Payload, true
	}
	for _, r := range a.Resources {
		if r.Name == resource {
			return r.Descriptor, true
		}
	}
	return artifactmodel.Descriptor{}, false
}

// revisionBoundSelection returns byte-free metadata with release-specific bundle paths removed.
// It copies resources but shares dependency slices and extension maps.
// Callers MUST keep shared metadata immutable.
func revisionBoundSelection(value artifactmodel.Artifact) artifactmodel.Artifact {
	value = value.Metadata()
	// Repackaging identical bytes does not create another artifact revision.
	value.Payload.BundlePath = ""
	for index := range value.Resources {
		value.Resources[index].BundlePath = ""
	}
	return value
}
