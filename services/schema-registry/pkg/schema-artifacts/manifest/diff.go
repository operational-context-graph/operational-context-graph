package manifest

import (
	"reflect"
	"sort"
	"strings"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	sourcemappingmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// Diff returns the deterministic conservative change summary for two manifests.
//
// The before and after manifests MUST be validated endpoints; Diff does not validate them or require adjacent releases.
//
// [Context.Artifacts] supplies exact bytes for schema, source mapping metadata, and added-alias proofs. Added source
// mapping descriptors also prove whether their source schema is newly introduced. Missing or unusable proof bytes
// produce a breaking impact rather than an error. [Context.RegistryOrigin] and [Context.History] are unused. An empty
// difference is operational, not initial.
//
// The result contains sorted, non-nil change collections and an empty [Changes.Summary].
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
			artifactChange := ArtifactChange{
				Operation:     OperationAdd,
				Kind:          right.Kind,
				ArtifactType:  right.Type,
				AfterRevision: right.Revision,
				Impact:        impact,
			}
			changes.Artifacts = append(changes.Artifacts, artifactChange)
		case !hasRight:
			artifactChange := ArtifactChange{
				Operation:      OperationRemove,
				Kind:           left.Kind,
				ArtifactType:   left.Type,
				BeforeRevision: left.Revision,
				Impact:         ImpactBreaking,
			}
			changes.Artifacts = append(changes.Artifacts, artifactChange)
		case left.Revision != right.Revision:
			// Non-breaking impacts require deterministic proof, never a publisher-supplied impact assertion.
			impact := ImpactBreaking
			if left.Kind == artifactmodel.KindSchema {
				impact = schemaImpact(left, right, context)
			} else if left.Kind == artifactmodel.KindSourceMapping && equivalentSourceMapping(left, right, context) {
				impact = ImpactOperational
			}
			artifactChange := ArtifactChange{
				Operation:      OperationUpdate,
				Kind:           right.Kind,
				ArtifactType:   right.Type,
				BeforeRevision: left.Revision,
				AfterRevision:  right.Revision,
				Impact:         impact,
			}
			changes.Artifacts = append(changes.Artifacts, artifactChange)
		}
	}
	changes.SchemaContextBindings = diffBindings(before, after)
	changes.URICatalog = diffURIs(before, after, context)
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
func addedMappingImpact(before Manifest, added artifactmodel.Artifact, context Context) Impact {
	bound, ok := context.Artifacts[artifactmodel.Dependency{Kind: added.Kind, Type: added.Type, Revision: added.Revision}]
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

// sameArtifactRelations compares metadata and resources independently of revision identity
// and packaging. It excludes primary payload descriptors, canonical URIs, creation versions, revisions,
// and bundle paths. Callers classify primary payload content separately; resource descriptors matter.
func sameArtifactRelations(left, right artifactmodel.Artifact) bool {
	left, right = revisionBoundSelection(left), revisionBoundSelection(right)
	left.Revision, right.Revision = "", ""
	left.CreatedUnderSchemaVersion, right.CreatedUnderSchemaVersion = "", ""
	left.Payload, right.Payload = artifactmodel.Descriptor{}, artifactmodel.Descriptor{}
	left.CanonicalURI, right.CanonicalURI = "", ""
	return reflect.DeepEqual(left, right)
}

// artifactMap indexes selected artifacts by kind and logical type using an unambiguous separator.
func artifactMap(manifest Manifest) map[string]artifactmodel.Artifact {
	result := make(map[string]artifactmodel.Artifact, len(manifest.Artifacts))
	for _, item := range manifest.Artifacts {
		result[string(item.Kind)+"\x00"+item.Type] = item
	}
	return result
}

// unionKeys returns the distinct logical artifact keys from both maps in canonical lexical order.
func unionKeys(left, right map[string]artifactmodel.Artifact) []string {
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

// diffBindings expands pairs and conservatively classifies additions affecting existing schemas.
func diffBindings(before, after Manifest) []BindingChange {
	left, right := bindingPairs(before), bindingPairs(after)
	result := make([]BindingChange, 0)
	for key := range left {
		if _, ok := right[key]; !ok {
			bindingChange := BindingChange{
				Operation:       OperationRemove,
				SchemaRevision:  left[key][0],
				ContextRevision: left[key][1],
				Impact:          ImpactBreaking,
			}
			result = append(result, bindingChange)
		}
	}
	for key := range right {
		if _, ok := left[key]; !ok {
			impact := ImpactBreaking
			if newRevisionIdentity(before, after, right[key][0]) {
				impact = ImpactAdditive
			}
			bindingChange := BindingChange{
				Operation:       OperationAdd,
				SchemaRevision:  right[key][0],
				ContextRevision: right[key][1],
				Impact:          impact,
			}
			result = append(result, bindingChange)
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
func diffURIs(before, after Manifest, context Context) []URIChange {
	unused := uriAdditionProof(before, context)
	introduced := make(map[string]string)
	previousArtifacts := artifactMap(before)
	for _, a := range after.Artifacts {
		if _, exists := previousArtifacts[string(a.Kind)+"\x00"+a.Type]; !exists {
			introduced[a.CanonicalURI] = a.Revision
		}
	}
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
			uriChange := URIChange{
				Operation:      OperationRemove,
				URI:            uri,
				BeforeRevision: entry.ArtifactRevision,
				BeforeResource: entry.Resource,
				Impact:         ImpactBreaking,
			}
			result = append(result, uriChange)
		} else if current.ArtifactRevision != entry.ArtifactRevision || current.Resource != entry.Resource {
			uriChange := URIChange{
				Operation:      OperationUpdate,
				URI:            uri,
				BeforeRevision: entry.ArtifactRevision,
				BeforeResource: entry.Resource,
				AfterRevision:  current.ArtifactRevision,
				AfterResource:  current.Resource,
				Impact:         ImpactBreaking,
			}
			result = append(result, uriChange)
		}
	}
	for uri, entry := range right {
		if _, ok := left[uri]; !ok {
			impact := ImpactBreaking
			// New canonical coordinates expose new logical artifacts, not requested aliases.
			// Every other addition needs safety proof independently of its target's identity.
			if (entry.Resource == "" && introduced[uri] == entry.ArtifactRevision) || unused(uri) {
				impact = ImpactAdditive
			}
			uriChange := URIChange{
				Operation:     OperationAdd,
				URI:           uri,
				AfterRevision: entry.ArtifactRevision,
				AfterResource: entry.Resource,
				Impact:        impact,
			}
			result = append(result, uriChange)
		}
	}
	return result
}

// diffStorage expands pairs and marks changed existing destinations as breaking.
func diffStorage(before, after Manifest) []StorageChange {
	left, right := storagePairs(before), storagePairs(after)
	result := make([]StorageChange, 0)
	for key, item := range left {
		if _, ok := right[key]; !ok {
			storageChange := StorageChange{
				Operation:    OperationRemove,
				Kind:         item.Kind,
				ArtifactType: item.ArtifactType,
				Store:        item.Store,
				Impact:       ImpactBreaking,
			}
			result = append(result, storageChange)
		}
	}
	for key, item := range right {
		if _, ok := left[key]; !ok {
			impact := ImpactBreaking
			if _, exists := findSelected(before.Artifacts, item.Kind, item.ArtifactType); !exists {
				if _, selected := findSelected(after.Artifacts, item.Kind, item.ArtifactType); selected {
					impact = ImpactAdditive
				}
			}
			storageChange := StorageChange{
				Operation:    OperationAdd,
				Kind:         item.Kind,
				ArtifactType: item.ArtifactType,
				Store:        item.Store,
				Impact:       impact,
			}
			result = append(result, storageChange)
		}
	}
	return result
}

// newRevisionIdentity reports whether revision belongs to a logical artifact absent from before.
// A new revision of an existing logical artifact does not count as a new identity.
func newRevisionIdentity(before, after Manifest, revision string) bool {
	for _, a := range after.Artifacts {
		if a.Revision == revision {
			_, exists := findSelected(before.Artifacts, a.Kind, a.Type)
			return !exists
		}
	}
	return false
}

// uriAdditionProof returns a predicate checking that an added URI cannot affect existing identifiers.
// It inspects existing documents once. Programs, unavailable bytes, or prefix overlap prevent proof.
// False means unsafe or unproven, not necessarily changed behavior.
//
// Callers MUST keep before and context immutable for the predicate's lifetime.
// The predicate memoizes its first inspection and MUST NOT be shared across concurrent callers.
func uriAdditionProof(before Manifest, context Context) func(string) bool {
	attempted, complete := false, false
	var prefixes []string
	return func(uri string) bool {
		if !attempted {
			prefixes, complete = existingURIPrefixes(before, context)
			attempted = true
		}
		if !complete {
			return false
		}
		// Prefixes are sorted and prefix-free, so only the closest preceding value can match.
		index := sort.SearchStrings(prefixes, uri)
		if index < len(prefixes) && prefixes[index] == uri {
			return false
		}
		return index == 0 || !strings.HasPrefix(uri, prefixes[index-1])
	}
}

// existingURIPrefixes returns sorted, prefix-free identifiers and whether before is fully inspected.
// False indicates missing bytes, invalid JSON, or opaque programs.
// Callers MUST treat incomplete inspection as unproven compatibility.
func existingURIPrefixes(before Manifest, context Context) ([]string, bool) {
	identifiers := make(map[string]bool)
	for _, a := range before.Artifacts {
		bound, exists := context.Artifacts[artifactmodel.Dependency{Kind: a.Kind, Type: a.Type, Revision: a.Revision}]
		if !exists || a.Kind == artifactmodel.KindSourceMapping {
			return nil, false
		}
		value, err := strictjson.Parse(bound.PayloadBytes)
		if err != nil {
			return nil, false
		}
		collectURIs(value, identifiers)
		for _, resource := range bound.Resources {
			if resource.Role == artifactmodel.ResourceRoleProgram {
				return nil, false
			}
			if !jsonMediaType(resource.MediaType) {
				continue
			}
			value, err := strictjson.Parse(resource.Bytes)
			if err != nil {
				return nil, false
			}
			collectURIs(value, identifiers)
		}
	}
	prefixes := make([]string, 0, len(identifiers))
	for uri := range identifiers {
		prefixes = append(prefixes, uri)
	}
	sort.Strings(prefixes)
	compact := prefixes[:0]
	for _, prefix := range prefixes {
		if len(compact) == 0 || !strings.HasPrefix(prefix, compact[len(compact)-1]) {
			compact = append(compact, prefix)
		}
	}
	return compact, true
}

// collectURIs adds profile-valid absolute URIs from JSON strings and member names to identifiers.
// The destination map MUST be initialized.
// Vocabulary bases are retained as prefixes for conservative alias checks.
func collectURIs(value any, identifiers map[string]bool) {
	switch v := value.(type) {
	case string:
		// A longer string cannot prefix any profile-accepted catalog key.
		if artifactmodel.ValidDocumentURI(v) {
			identifiers[v] = true
		}
	case map[string]any:
		for key, child := range v {
			collectURIs(key, identifiers)
			collectURIs(child, identifiers)
		}
	case []any:
		for _, child := range v {
			collectURIs(child, identifiers)
		}
	}
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
