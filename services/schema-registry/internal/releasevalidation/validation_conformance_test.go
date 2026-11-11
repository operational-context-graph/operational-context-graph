package releasevalidation

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/test/shared/corpus"
	"github.com/stretchr/testify/require"
)

// buildCorpusRelease resolves complete snapshots and computes integrity metadata from the checked-in exact bytes.
// It neither populates storage nor performs release activation.
func buildCorpusRelease(t testing.TB, set corpus.Corpus, name string) (manifest.Manifest, manifest.Context) {
	t.Helper()
	var recipe *corpus.Release
	for i := range set.Releases {
		if set.Releases[i].Name == name {
			recipe = &set.Releases[i]
		}
	}
	require.NotNil(t, recipe, "unknown corpus release %s", name)
	m := manifest.Manifest{Schema: set.Schema, FormatVersion: manifest.FormatVersion, SchemaVersion: recipe.Version,
		Source: manifest.Source{Repository: set.Source.Repository, Commit: set.Source.Commit}}
	c := manifest.Context{RegistryOrigin: set.Origin, Artifacts: make(map[artifact.Dependency]artifact.Artifact)}
	if recipe.Parent != "" {
		parent, context := buildCorpusRelease(t, set, recipe.Parent)
		m.Artifacts = slices.Clone(parent.Artifacts)
		m.URICatalog = slices.Clone(parent.URICatalog)
		m.StorageCatalog = slices.Clone(parent.StorageCatalog)
		m.SchemaContextBindings = slices.Clone(parent.SchemaContextBindings)
		c.Artifacts = maps.Clone(context.Artifacts)
		c.History = append(slices.Clone(context.History), manifest.Release{Manifest: parent, Digest: corpusDigest(parent.Raw)})
		m.PreviousRelease = &manifest.PreviousRelease{SchemaVersion: parent.SchemaVersion, ManifestDigest: corpusDigest(parent.Raw)}
	}
	for _, definition := range recipe.Artifacts {
		item := artifact.Artifact{
			Kind: artifact.Kind(definition.Kind), Type: definition.Type, Revision: definition.Revision,
			CreatedUnderSchemaVersion: recipe.Version, SchemaClass: artifact.SchemaClass(definition.Class), TargetSchema: definition.TargetVersion,
			PayloadBytes: []byte(definition.Payload), Resources: []artifact.Resource{}, Dependencies: []artifact.Dependency{},
		}
		if definition.PayloadFile != "" {
			item.PayloadBytes = corpus.ReadFixture(t, definition.PayloadFile)
		}
		mediaType := artifact.SchemaMediaType
		switch item.Kind {
		case artifact.KindContext:
			mediaType = artifact.ContextMediaType
		case artifact.KindSourceMapping:
			mediaType = sourcemapping.DescriptorMediaType
		}
		prefix := string(item.Kind) + "/" + item.Type + "/"
		item.Payload = testDescriptor(item.PayloadBytes, mediaType, prefix+"payload.json")
		item.CanonicalURI = set.Origin + "/v1/resources/" + prefix + item.Revision
		for _, resource := range definition.Resources {
			item.Resources = append(item.Resources, artifact.Resource{
				Name: resource.Name, Role: artifact.ResourceRole(resource.Role), Bytes: resource.Content,
				Descriptor: testDescriptor(resource.Content, resource.MediaType, prefix+resource.Name),
			})
		}
		for _, dependency := range definition.Dependencies {
			var found bool
			for _, selected := range m.Artifacts {
				if selected.Kind == artifact.KindSchema && selected.Type == dependency {
					item.Dependencies = append(item.Dependencies, artifact.Dependency{Kind: selected.Kind, Type: selected.Type, Revision: selected.Revision})
					found = true
				}
			}
			require.True(t, found, "fixture dependency %s is missing", dependency)
		}
		c.Artifacts[corpusKey(item)] = item
		m.Artifacts = append(m.Artifacts, item.Metadata())
		m.URICatalog = append(m.URICatalog, manifest.URIEntry{URI: item.CanonicalURI, ArtifactRevision: item.Revision})
		if len(definition.Stores) > 0 {
			stores := make([]manifest.Store, len(definition.Stores))
			for i, store := range definition.Stores {
				stores[i] = manifest.Store(store)
			}
			m.StorageCatalog = append(m.StorageCatalog, manifest.StorageEntry{Kind: artifact.KindSchema, ArtifactType: item.Type, Stores: stores})
		}
		if len(definition.Contexts) > 0 {
			m.SchemaContextBindings = append(m.SchemaContextBindings, manifest.Binding{SchemaRevision: item.Revision, ContextRevisions: definition.Contexts})
		}
	}
	changes := manifest.Changes{Compatibility: manifest.CompatibilityInitial, Artifacts: []manifest.ArtifactChange{}, SchemaContextBindings: []manifest.BindingChange{}, URICatalog: []manifest.URIChange{}, StorageCatalog: []manifest.StorageChange{}}
	if len(c.History) > 0 {
		var err error
		changes, err = manifest.Diff(c.History[len(c.History)-1].Manifest, m, c)
		require.NoError(t, err)
	}
	require.Equal(t, manifest.Compatibility(recipe.Compatibility), changes.Compatibility)
	m.Changes = &changes
	data, err := manifest.Canonicalize(corpusJSON(t, corpusDocument(m)))
	require.NoError(t, err)
	m, err = manifest.Parse(data)
	require.NoError(t, err)
	return m, c
}

func corpusKey(a artifact.Artifact) artifact.Dependency {
	return artifact.Dependency{Kind: a.Kind, Type: a.Type, Revision: a.Revision}
}

func corpusDigest(data []byte) string {
	return testDescriptor(data, "application/octet-stream", "unused").Digest
}

func corpusJSON(t testing.TB, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func corpusDescriptor(d artifact.Descriptor) map[string]any {
	return map[string]any{"mediaType": d.MediaType, "digest": d.Digest, "size": d.Size, "bundlePath": d.BundlePath}
}

// corpusDocument spells the wire contract independently of the parser's internal field names.
func corpusDocument(m manifest.Manifest) map[string]any {
	artifacts, bindings, uris, storage := []any{}, []any{}, []any{}, []any{}
	for _, a := range m.Artifacts {
		resources, dependencies := []any{}, []any{}
		for _, r := range a.Resources {
			d := corpusDescriptor(r.Descriptor)
			d["name"], d["role"] = r.Name, r.Role
			resources = append(resources, d)
		}
		for _, d := range a.Dependencies {
			dependencies = append(dependencies, map[string]any{"kind": d.Kind, "artifactType": d.Type, "artifactRevision": d.Revision})
		}
		item := map[string]any{"kind": a.Kind, "artifactType": a.Type, "artifactRevision": a.Revision, "createdUnderSchemaVersion": a.CreatedUnderSchemaVersion,
			"canonicalUri": a.CanonicalURI, "payload": corpusDescriptor(a.Payload), "resources": resources, "dependencies": dependencies}
		if a.SchemaClass != "" {
			item["schemaClass"] = a.SchemaClass
		}
		if a.TargetSchema != "" {
			item["targetSchemaVersion"] = a.TargetSchema
		}
		if a.Extensions != nil {
			item["extensions"] = a.Extensions
		}
		artifacts = append(artifacts, item)
	}
	for _, b := range m.SchemaContextBindings {
		bindings = append(bindings, map[string]any{"schemaRevision": b.SchemaRevision, "contextRevisions": b.ContextRevisions})
	}
	for _, u := range m.URICatalog {
		entry := map[string]any{"uri": u.URI, "artifactRevision": u.ArtifactRevision}
		if u.Resource != "" {
			entry["resource"] = u.Resource
		}
		uris = append(uris, entry)
	}
	for _, s := range m.StorageCatalog {
		storage = append(storage, map[string]any{"kind": s.Kind, "artifactType": s.ArtifactType, "stores": s.Stores})
	}
	doc := map[string]any{"$schema": m.Schema, "formatVersion": m.FormatVersion, "schemaVersion": m.SchemaVersion, "previousRelease": nil,
		"source": map[string]any{"repository": m.Source.Repository, "commit": m.Source.Commit}, "artifacts": artifacts, "schemaContextBindings": bindings, "uriCatalog": uris, "storageCatalog": storage}
	if m.PreviousRelease != nil {
		doc["previousRelease"] = map[string]any{"schemaVersion": m.PreviousRelease.SchemaVersion, "manifestDigest": m.PreviousRelease.ManifestDigest}
	}
	if m.Changes != nil {
		c := m.Changes
		doc["changes"] = map[string]any{"compatibility": c.Compatibility, "artifacts": c.Artifacts, "schemaContextBindings": c.SchemaContextBindings, "uriCatalog": c.URICatalog, "storageCatalog": c.StorageCatalog}
	}
	return doc
}

// TestReleaseCorpusValidSnapshots exercises complete fixtures, candidate coexistence, and conservative compatibility without executing programs.
func TestReleaseCorpusValidSnapshots(t *testing.T) {
	set := corpus.LoadCorpus(t)
	for _, recipe := range set.Releases {
		t.Run(recipe.Name, func(t *testing.T) {
			m, c := buildCorpusRelease(t, set, recipe.Name)
			require.NoError(t, Validate(m, c))
			parsed, err := manifest.Validate(m.Raw, c)
			require.NoError(t, err)
			require.Equal(t, m, parsed)
			if recipe.Parent != "" {
				changes, err := manifest.Diff(c.History[len(c.History)-1].Manifest, m, c)
				require.NoError(t, err)
				require.Equal(t, manifest.Compatibility(recipe.Compatibility), changes.Compatibility)
			}
		})
	}
	m, c := buildCorpusRelease(t, set, "multi-candidate")
	closure := indexClosure(m, c)
	a, err := closure.mappingPackage(closure.selections[mappingKey("mapping-a")])
	require.NoError(t, err)
	require.Equal(t, []string{"source", "edge", "entity", "identifier"}, schemaTypes(a.Schemas))
	require.Equal(t, "z.bin", a.Detail.Transformations[0].ProgramName)
	require.Len(t, a.Detail.Transformations, 4)
	require.Len(t, a.Programs, 3)
	b, err := closure.mappingPackage(closure.selections[mappingKey("mapping-b")])
	require.NoError(t, err)
	require.Equal(t, a.Detail.Condition.ProgramDigest, b.Detail.Condition.ProgramDigest, "identical opaque conditions may overlap")
	without, err := closure.mappingPackage(closure.selections[mappingKey("mapping-c")])
	require.NoError(t, err)
	require.Nil(t, without.Detail.Condition)
	const workers = 8
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() { failures <- Validate(m, c) })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

// mutateCorpusJSON applies a single declarative fixture mutation. Paths use object keys or zero-based array indices.
func mutateCorpusJSON(t testing.TB, doc map[string]any, path, operation string, value any) {
	t.Helper()
	parts := strings.Split(path, "/")
	var current any = doc
	for _, part := range parts[:len(parts)-1] {
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			require.NoError(t, err)
			current = node[i]
		default:
			t.Fatalf("invalid corpus path %s", path)
		}
	}
	object, ok := current.(map[string]any)
	require.True(t, ok, "corpus path must end in an object field: %s", path)
	key := parts[len(parts)-1]
	switch operation {
	case "set":
		object[key] = value
	case "delete":
		delete(object, key)
	case "duplicate":
		array := object[key].([]any)
		object[key] = append(array, array[0])
	case "append":
		object[key] = append(object[key].([]any), value)
	case "repeat":
		original := object[key].([]any)[0]
		values := make([]any, int(value.(float64)))
		for i := range values {
			values[i] = original
		}
		object[key] = values
	case "string":
		object[key] = strings.Repeat("a", int(value.(float64)))
	case "uri":
		const prefix = "https://example.test/"
		object[key] = prefix + strings.Repeat("a", int(value.(float64))-len(prefix))
	case "reverse":
		slices.Reverse(object[key].([]any))
	default:
		t.Fatalf("unknown corpus operation %s", operation)
	}
}

func corpusFailure(data []byte, c manifest.Context) (string, error) {
	m, err := manifest.Parse(data)
	if err != nil {
		return "parse", err
	}
	if err := m.Validate(c); err != nil {
		return "closure", err
	}
	if err := Validate(m, c); err != nil {
		return "serviceability", err
	}
	return "accepted", nil
}

func corpusClassification(err error) string {
	for _, candidate := range []struct {
		name     string
		sentinel error
	}{
		{"duplicate-member", strictjson.ErrDuplicate}, {"byte-limit", strictjson.ErrTooLarge},
		{"depth-limit", strictjson.ErrDepth}, {"member-limit", strictjson.ErrObjectMembers},
		{"array-limit", strictjson.ErrArrayEntries}, {"string-limit", strictjson.ErrStringTooLarge}, {"number", strictjson.ErrNumber},
		{"invalid-manifest", manifest.ErrInvalid}, {"malformed-json", strictjson.ErrMalformed}, {"response-limit", httpapi.ErrTooLarge},
	} {
		if errors.Is(err, candidate.sentinel) {
			return candidate.name
		}
	}
	return "unclassified"
}

// TestReleaseCorpusFailures requires the declared phase, sentinel classification, and repeatable diagnostic for every invalid recipe.
func TestReleaseCorpusFailures(t *testing.T) {
	set := corpus.LoadCorpus(t)
	cases := corpus.LoadFailures(t)
	rules := map[string]bool{}
	for _, line := range strings.Split(string(corpus.ReadFixture(t, "README.md")), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "`")
		require.False(t, rules[parts[1]], "duplicate traceability rule %s", parts[1])
		rules[parts[1]] = true
	}
	covered := map[string]bool{}
	seen := map[string]bool{}
	for _, test := range cases {
		require.True(t, rules[test.Rule], "unknown traceability rule %s", test.Rule)
		covered[test.Rule] = true
		require.False(t, seen[test.ID], "duplicate case ID %s", test.ID)
		seen[test.ID] = true
		t.Run(test.ID, func(t *testing.T) {
			require.NotEmpty(t, test.Rule)
			require.NotEmpty(t, test.ErrorContains)
			name := test.Release
			if name == "" {
				name = "multi-candidate"
			}
			m, c := buildCorpusRelease(t, set, name)
			require.NoError(t, Validate(m, c), "the unmodified fixture MUST pass all phases")
			data := mutateCorpus(t, m, c, test)
			phase, err := corpusFailure(data, c)
			require.Error(t, err)
			require.Equal(t, test.Phase, phase)
			require.Equal(t, test.Classification, corpusClassification(err))
			require.ErrorContains(t, err, test.ErrorContains)
			repeatedPhase, repeated := corpusFailure(data, c)
			require.Equal(t, phase, repeatedPhase)
			require.EqualError(t, repeated, err.Error())
		})
	}
	require.Equal(t, rules, covered, "every normative rejection rule MUST have an invalid fixture")
}

func mutateCorpus(t testing.TB, m manifest.Manifest, c manifest.Context, test corpus.FailureCase) []byte {
	t.Helper()
	name := test.Artifact
	if name == "" {
		name = "mapping-b"
	}
	key := mappingKey(name)
	for _, a := range m.Artifacts {
		if a.Type == name {
			key = corpusKey(a)
		}
	}
	item := c.Artifacts[key]
	doc := corpusDocument(m)
	// Decode the independent wire representation so every array has []any elements.
	require.NoError(t, json.Unmarshal(corpusJSON(t, doc), &doc))
	switch test.Target {
	case "release-version":
		original := m.SchemaVersion
		m.SchemaVersion = test.Value.(string)
		for i := range m.Artifacts {
			a := &m.Artifacts[i]
			if a.CreatedUnderSchemaVersion != original {
				continue
			}
			a.CreatedUnderSchemaVersion = m.SchemaVersion
			bound := c.Artifacts[corpusKey(*a)]
			bound.CreatedUnderSchemaVersion = m.SchemaVersion
			c.Artifacts[corpusKey(*a)] = bound
		}
		doc = corpusDocument(m)
	case "manifest":
		mutateCorpusJSON(t, doc, test.Path, test.Operation, test.Value)
	case "payload":
		rewriteMapping(t, &item, func(payload map[string]any) { mutateCorpusJSON(t, payload, test.Path, test.Operation, test.Value) })
		updateArtifact(&m, c, item)
		doc = corpusDocument(m)
	case "payload-bytes":
		item.PayloadBytes = []byte(test.Value.(string))
		item.Payload = testDescriptor(item.PayloadBytes, item.Payload.MediaType, item.Payload.BundlePath)
		updateArtifact(&m, c, item)
		doc = corpusDocument(m)
	case "bound":
		for i, a := range m.Artifacts {
			if corpusKey(a) == key {
				mutateCorpusJSON(t, doc, fmt.Sprintf("artifacts/%d/%s", i, test.Path), test.Operation, test.Value)
			}
		}
		encoded, err := jcs.Transform(corpusJSON(t, doc))
		require.NoError(t, err)
		parsed, err := manifest.Parse(encoded)
		require.NoError(t, err, "bound mutations MUST pass structural validation")
		for _, a := range parsed.Artifacts {
			if corpusKey(a) != key {
				continue
			}
			a.PayloadBytes = item.PayloadBytes
			for i := range a.Resources {
				for _, r := range item.Resources {
					if a.Resources[i].Name == r.Name {
						a.Resources[i].Bytes = r.Bytes
					}
				}
			}
			c.Artifacts[key] = a
		}
	case "missing-bytes":
		delete(c.Artifacts, key)
	case "corrupt-resource":
		item.Resources = slices.Clone(item.Resources)
		item.Resources[0].Bytes = []byte("corrupt")
		c.Artifacts[key] = item
	case "large-package":
		oversizeMapping(t, &m, c, name)
		doc = corpusDocument(m)
	case "raw":
		switch test.Operation {
		case "bom":
			return append([]byte{0xef, 0xbb, 0xbf}, m.Raw...)
		case "newline":
			return append(slices.Clone(m.Raw), '\n')
		case "duplicate":
			return append([]byte(`{"schemaVersion":"1.0.0",`), m.Raw[1:]...)
		case "empty":
			return nil
		case "utf8":
			return []byte{'{', '"', 0xff, '"', ':', '1', '}'}
		default:
			t.Fatalf("unknown raw fixture operation %s", test.Operation)
		}
	case "profile":
		switch test.Operation {
		case "members":
			members := map[string]any{}
			for i := range schemaartifacts.MaxObjectMembers + 1 {
				members[fmt.Sprintf("org.example.member%d", i)] = true
			}
			doc["extensions"] = members
		case "depth":
			var nested any = true
			for range schemaartifacts.MaxJSONDepth {
				nested = []any{nested}
			}
			doc["extensions"] = map[string]any{"org.example.deep": nested}
		case "array":
			doc["uriCatalog"] = make([]any, schemaartifacts.MaxArrayEntries+1)
		case "string":
			doc["extensions"] = map[string]any{"org.example.long": strings.Repeat("a", schemaartifacts.MaxJSONStringBytes+1)}
		case "bytes":
			return []byte(strings.Repeat(" ", schemaartifacts.MaxArtifactBytes+1))
		default:
			t.Fatalf("unknown profile operation %s", test.Operation)
		}
	default:
		t.Fatalf("unknown corpus target %s", test.Target)
	}
	data, err := jcs.Transform(corpusJSON(t, doc))
	require.NoError(t, err)
	return data
}
