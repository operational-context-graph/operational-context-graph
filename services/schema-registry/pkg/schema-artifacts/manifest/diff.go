package manifest

import (
	"sort"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	sourcemappingmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

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
