package httpapi

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/pagination"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
)

// artifactTestReader supplies controlled artifact state to each in-process route request.
type artifactTestReader struct{ snapshot *artifactTestSnapshot }

// Read exposes the fixture snapshot without opening a database transaction.
func (r artifactTestReader) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	return fn(ctx, r.snapshot)
}

// artifactTestSnapshot models visible releases, immutable artifacts, selected relationships, filters, and injected lookup failures.
type artifactTestSnapshot struct {
	current       model.CurrentState
	releases      map[string]model.Release
	artifacts     map[string]model.Artifact
	selections    map[string]model.Artifact
	collections   map[string][]model.Artifact
	history       []model.ReleaseArtifact
	resources     map[string][]model.Resource
	dependencies  map[string][]model.Dependency
	mappings      map[string]model.SourceMappingDetail
	uris          map[string]model.URIEntry
	bindings      map[string][]model.SchemaContextBinding
	stores        map[string][]model.StorageEntry
	artifactErr   error
	resourceErr   error
	dependencyErr error
}

// CurrentState supplies the current pointer and immutable activation ceiling.
func (s *artifactTestSnapshot) CurrentState() model.CurrentState { return s.current }

// CurrentRelease resolves the fixture's current version through the same visibility rules as selected releases.
func (s *artifactTestSnapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	return s.Release(ctx, s.current.SchemaVersion)
}

// Release returns configured visible releases and hides absent versions behind ErrNotFound.
func (s *artifactTestSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	value, ok := s.releases[version]
	if !ok {
		return model.Release{}, postgres.ErrNotFound
	}
	return value, nil
}

// ReleasesAt supplies an empty release collection outside artifact-route fixture concerns.
func (*artifactTestSnapshot) ReleasesAt(context.Context, *int64, int64, int) ([]model.Release, error) {
	return nil, nil
}

// IsAncestor supplies no ancestry evidence because these fixtures do not support release comparisons.
func (*artifactTestSnapshot) IsAncestor(context.Context, string, string) (bool, error) {
	return false, nil
}

// Artifact supplies immutable revision content or a controlled absence or integrity failure.
func (s *artifactTestSnapshot) Artifact(_ context.Context, revision string) (model.Artifact, error) {
	if s.artifactErr != nil {
		return model.Artifact{}, s.artifactErr
	}
	value, ok := s.artifacts[revision]
	if !ok {
		return model.Artifact{}, postgres.ErrNotFound
	}
	return value, nil
}

// ReleaseArtifact resolves the fixture's logical selections; release visibility is checked separately by the service.
func (s *artifactTestSnapshot) ReleaseArtifact(_ context.Context, _ string, kind, artifactType string) (model.Artifact, error) {
	if s.artifactErr != nil {
		return model.Artifact{}, s.artifactErr
	}
	value, ok := s.selections[kind+"/"+artifactType]
	if !ok {
		return model.Artifact{}, postgres.ErrNotFound
	}
	return value, nil
}

// ReleaseArtifacts emulates exact artifact and resolved-mapping filters, canonical ordering, and exclusive kind/type continuation bounds.
func (s *artifactTestSnapshot) ReleaseArtifacts(_ context.Context, version string, filter model.ArtifactFilter, afterKind, afterType *string, limit int) ([]model.Artifact, error) {
	items, ok := s.collections[version]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	filtered := make([]model.Artifact, 0, len(items))
	for _, item := range items {
		if filter.Kind != "" && item.Kind != filter.Kind ||
			filter.ArtifactType != "" && item.Type != filter.ArtifactType ||
			filter.SchemaClass != "" && (item.SchemaClass == nil || *item.SchemaClass != filter.SchemaClass) {
			continue
		}
		if filter.DependsOnRevision != "" {
			found := false
			for _, dependency := range s.dependencies[item.Revision] {
				found = dependency.Revision == filter.DependsOnRevision
			}
			if !found {
				continue
			}
		}
		if item.Kind == "source-mapping" {
			detail := s.mappings[item.Revision]
			if filter.SourceArtifactType != "" && detail.SourceArtifactType != filter.SourceArtifactType || filter.EngineLanguage != "" && detail.EngineLanguage != filter.EngineLanguage || filter.ExecutionProfile != "" && detail.ExecutionProfile != filter.ExecutionProfile {
				continue
			}
			if filter.TargetArtifactType != "" {
				matched := false
				for _, transformation := range detail.Transformations {
					matched = matched || transformation.TargetArtifactType == filter.TargetArtifactType
				}
				if !matched {
					continue
				}
			}
		}
		if afterKind != nil && (item.Kind < *afterKind || item.Kind == *afterKind && item.Type <= *afterType) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(first, second int) bool {
		if filtered[first].Kind == filtered[second].Kind {
			return filtered[first].Type < filtered[second].Type
		}
		return filtered[first].Kind < filtered[second].Kind
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// ArtifactHistory supplies descending logical selections within the original activation ceiling and exclusive continuation position.
func (s *artifactTestSnapshot) ArtifactHistory(_ context.Context, kind, artifactType string, before *int64, maximum int64, limit int) ([]model.ReleaseArtifact, error) {
	items := make([]model.ReleaseArtifact, 0, len(s.history))
	for _, item := range s.history {
		if item.Kind != kind || item.ArtifactType != artifactType || item.ActivationSequence > maximum || before != nil && item.ActivationSequence >= *before {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(first, second int) bool {
		return items[first].ActivationSequence > items[second].ActivationSequence
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// Resources copies fixture resource metadata or injects a controlled resource-query failure.
func (s *artifactTestSnapshot) Resources(_ context.Context, revision string) ([]model.Resource, error) {
	if s.resourceErr != nil {
		return nil, s.resourceErr
	}
	return append([]model.Resource(nil), s.resources[revision]...), nil
}

// Resource returns named fixture content with independent failure injection and missing-resource classification.
func (s *artifactTestSnapshot) Resource(_ context.Context, revision, name string) (model.Resource, error) {
	if s.resourceErr != nil {
		return model.Resource{}, s.resourceErr
	}
	for _, value := range s.resources[revision] {
		if value.Name == name {
			return value, nil
		}
	}
	return model.Resource{}, postgres.ErrNotFound
}

// Dependencies copies exact-revision fixture relationships or injects a dependency-query failure.
func (s *artifactTestSnapshot) Dependencies(_ context.Context, revision string) ([]model.Dependency, error) {
	if s.dependencyErr != nil {
		return nil, s.dependencyErr
	}
	return append([]model.Dependency(nil), s.dependencies[revision]...), nil
}

// ContextBindings supplies copied context selections scoped to both release and schema revision.
func (s *artifactTestSnapshot) ContextBindings(_ context.Context, version, revision string) ([]model.SchemaContextBinding, error) {
	return append([]model.SchemaContextBinding(nil), s.bindings[version+"\x00"+revision]...), nil
}

// StorageEntries supplies copied logical placements scoped to release and schema type.
func (s *artifactTestSnapshot) StorageEntries(_ context.Context, version, artifactType string) ([]model.StorageEntry, error) {
	return append([]model.StorageEntry(nil), s.stores[version+"\x00"+artifactType]...), nil
}

// URIEntry performs exact release-scoped fixture lookup without normalizing the requested URI.
func (s *artifactTestSnapshot) URIEntry(_ context.Context, version, uri string) (model.URIEntry, error) {
	value, ok := s.uris[version+"\x00"+uri]
	if !ok {
		return model.URIEntry{}, postgres.ErrNotFound
	}
	return value, nil
}

// SourceMappingByRevision supplies mapping projections by exact revision for agreement checks.
func (s *artifactTestSnapshot) SourceMappingByRevision(_ context.Context, _ string, revision string) (model.SourceMappingDetail, error) {
	value, ok := s.mappings[revision]
	if !ok {
		return model.SourceMappingDetail{}, postgres.ErrNotFound
	}
	return value, nil
}

// SourceMapping provides legacy source-type fixture lookup; candidate collection tests use revision-resolved mapping projections instead.
func (s *artifactTestSnapshot) SourceMapping(_ context.Context, _ string, source string) (model.SourceMappingDetail, error) {
	for _, value := range s.mappings {
		if value.SourceArtifactType == source {
			return value, nil
		}
	}
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// TestCommonArtifactDescriptorsCoverKindsRelationshipsAndImmutableReuse verifies all artifact kinds expose complete relationships and immutable revisions retain release-independent identity.
func TestCommonArtifactDescriptorsCoverKindsRelationshipsAndImmutableReuse(t *testing.T) {
	snapshot := artifactFixture()
	server := artifactTestServer(snapshot)

	// Selected schemas expose complete integrity, resource, dependency, extension, and relationship metadata without importer-only bundle paths.
	t.Run("release scoped schema descriptor", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/schema/device")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.ArtifactDescriptor
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		assert.Equal(t, artifactmodel.SchemaClassEntity, *body.SchemaClass)
		assert.Nil(t, body.TargetSchemaVersion)
		assert.Equal(t, "/v1/artifacts/rev-schema", body.Payload.Href)
		assert.Len(t, body.Resources, 1)
		assert.Equal(t, "/v1/artifacts/rev-schema/resources/example", body.Resources[0].Href)
		assert.Equal(t, "/v1/releases/1.0.0/artifacts/context/device", body.Dependencies[0].Href)
		assert.Equal(t, "/v1/releases/1.0.0/schemas/device/contexts", body.Links["contexts"])
		assert.Equal(t, "/v1/releases/1.0.0/schemas/device/package", body.Links["package"])
		assert.Equal(t, json.RawMessage(`{"org.example.review":{"approved":true}}`), body.Extensions)
		assert.NotContains(t, response.Body.String(), "bundlePath")
	})

	// Contexts omit schema classification, target version, and schema-only context/package links.
	t.Run("context descriptor omits schema relationships", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/context/device")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.ArtifactDescriptor
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		assert.Nil(t, body.SchemaClass)
		assert.Nil(t, body.TargetSchemaVersion)
		assert.NotContains(t, body.Links, "contexts")
		assert.NotContains(t, body.Links, "package")
	})

	// Mapping metadata exposes its target schema version and a package link keyed by the mapping's own logical type.
	t.Run("source mapping descriptor uses source keyed package link", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/source-mapping/device-mapping")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.ArtifactDescriptor
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.NotNil(t, body.TargetSchemaVersion)
		assert.Equal(t, "1.0.0", *body.TargetSchemaVersion)
		assert.Nil(t, body.SchemaClass)
		assert.Equal(t, "/v1/releases/1.0.0/source-mappings/device-mapping/package", body.Links["package"])
	})

	// Immutable revision metadata uses revision-specific links and omits all release-dependent schema relationship links.
	t.Run("immutable reused descriptor omits release links", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-schema/metadata")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.ArtifactDescriptor
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		assert.Equal(t, "/v1/artifacts/rev-schema/metadata", body.Links["self"])
		assert.Equal(t, "/v1/artifacts/rev-context/metadata", body.Dependencies[0].Href)
		assert.NotContains(t, body.Links, "contexts")
		assert.NotContains(t, body.Links, "package")
		assert.NotContains(t, body.Links, "sourceMappings")
		assert.NotContains(t, body.Links, "stores")
	})

	// Reusing a revision preserves payload identity while each selected descriptor retains its own release links and version header.
	t.Run("reused revision remains immutable across release selections", func(t *testing.T) {
		snapshot.releases["1.1.0"] = model.Release{SchemaVersion: "1.1.0", ActivationSequence: 2}
		first := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/schema/device")
		second := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.1.0/artifacts/schema/device")
		require.Equal(t, http.StatusOK, first.Code)
		require.Equal(t, http.StatusOK, second.Code)
		var before, after v1.ArtifactDescriptor
		require.NoError(t, json.Unmarshal(first.Body.Bytes(), &before))
		require.NoError(t, json.Unmarshal(second.Body.Bytes(), &after))
		assert.Equal(t, before.ArtifactRevision, after.ArtifactRevision)
		assert.Equal(t, before.Payload, after.Payload)
		assert.Equal(t, "/v1/releases/1.1.0/artifacts/schema/device", after.Links["self"])
		assert.Equal(t, "1.1.0", second.Header().Get("Selected-Schema-Version"))
	})
}

// TestCommonArtifactDescriptorsRejectInactiveAndInconsistentRows verifies inactive-only revisions return 404 and corrupt resource or dependency evidence returns inconsistent-registry-state.
func TestCommonArtifactDescriptorsRejectInactiveAndInconsistentRows(t *testing.T) {
	// A revision stored only for an inactive release is not visible through immutable metadata lookup.
	t.Run("inactive only revision is hidden", func(t *testing.T) {
		response := serveArtifactRequest(artifactTestServer(artifactFixture()), http.MethodGet, "/v1/artifacts/inactive/metadata")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"artifact-not-found"`)
	})

	// Resource projection integrity failures produce inconsistent-registry-state instead of artifact metadata.
	t.Run("inconsistent resources are not represented", func(t *testing.T) {
		snapshot := artifactFixture()
		snapshot.resourceErr = postgres.ErrInconsistentState
		response := serveArtifactRequest(artifactTestServer(snapshot), http.MethodGet, "/v1/releases/1.0.0/artifacts/schema/device")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	})

	// Dependency projection integrity failures produce inconsistent-registry-state instead of artifact metadata.
	t.Run("inconsistent dependencies are not represented", func(t *testing.T) {
		snapshot := artifactFixture()
		snapshot.dependencyErr = postgres.ErrInconsistentState
		response := serveArtifactRequest(artifactTestServer(snapshot), http.MethodGet, "/v1/artifacts/rev-schema/metadata")
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"inconsistent-registry-state"`)
	})
}

// TestReleaseArtifactCollectionFiltersPaginationAndValidation verifies exact selection filters, bounded pages, and rejection of invalid values or incompatible filter combinations.
func TestReleaseArtifactCollectionFiltersPaginationAndValidation(t *testing.T) {
	snapshot := artifactFixture()
	server := artifactTestServer(snapshot)

	// Complete collections preserve canonical kind order, null continuation, and HEAD validator parity without a body.
	t.Run("returns ordered complete descriptors and supports HEAD", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.Page[v1.ArtifactDescriptor]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Len(t, body.Items, 3)
		assert.Equal(t, []artifactmodel.Kind{artifactmodel.KindContext, artifactmodel.KindSchema, artifactmodel.KindSourceMapping}, []artifactmodel.Kind{body.Items[0].Kind, body.Items[1].Kind, body.Items[2].Kind})
		assert.Nil(t, body.NextCursor)
		head := serveArtifactRequest(server, http.MethodHead, "/v1/releases/1.0.0/artifacts")
		assert.Equal(t, http.StatusOK, head.Code)
		assert.Equal(t, response.Header().Get("ETag"), head.Header().Get("ETag"))
		assert.Empty(t, head.Body.String())
	})

	// Kind, type, class, and dependency predicates combine conjunctively to select the exact matching revision.
	t.Run("combines exact filters with logical AND", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?kind=schema&artifactType=device&schemaClass=entity-type&dependsOnRevision=rev-context")
		require.Equal(t, http.StatusOK, response.Code)
		var body v1.Page[v1.ArtifactDescriptor]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Len(t, body.Items, 1)
		assert.Equal(t, "rev-schema", body.Items[0].ArtifactRevision)
		assert.Nil(t, body.NextCursor)
	})

	// A valid unmatched filter returns an empty array and null continuation rather than a missing-item error.
	t.Run("well formed unmatched type returns an empty collection", func(t *testing.T) {
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?artifactType=unknown")
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"items":[]`)
		assert.Contains(t, response.Body.String(), `"nextCursor":null`)
	})

	// A collection exceeding the maximum page length returns exactly 1000 descriptors and a continuation token.
	t.Run("returns a 1000 item page and a next cursor", func(t *testing.T) {
		items := make([]model.Artifact, 1001)
		for index := range items {
			items[index] = fixtureArtifact("revision-"+strconv.Itoa(index), "schema", "item-"+fmt.Sprintf("%04d", index), new("entity-type"), nil, []byte("true"))
			snapshot.artifacts[items[index].Revision] = items[index]
			snapshot.resources[items[index].Revision] = []model.Resource{}
			snapshot.dependencies[items[index].Revision] = []model.Dependency{}
		}
		snapshot.collections["1.0.0"] = items
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?limit=1000")
		require.Equal(t, http.StatusOK, response.Code)
		var body struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor *string           `json:"nextCursor"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		assert.Len(t, body.Items, 1000)
		assert.NotNil(t, body.NextCursor)
	})

	for _, test := range []struct{ name, path, code string }{
		{"invalid filter combination", "/v1/releases/1.0.0/artifacts?schemaClass=entity-type", "invalid-filter-combination"},
		{"unsupported kind", "/v1/releases/1.0.0/artifacts?kind=unknown", "unsupported-artifact-kind"},
		{"unsupported schema class", "/v1/releases/1.0.0/artifacts?kind=schema&schemaClass=unknown", "unsupported-schema-class"},
		{"malformed type", "/v1/releases/1.0.0/artifacts?artifactType=UPPER", "invalid-query-parameter"},
		{"malformed dependency", "/v1/releases/1.0.0/artifacts?dependsOnRevision=..", "invalid-query-parameter"},
		{"duplicate query", "/v1/releases/1.0.0/artifacts?kind=schema&kind=context", "invalid-query-parameter"},
		{"invalid limit", "/v1/releases/1.0.0/artifacts?limit=1001", "invalid-query-parameter"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
		})
	}
}

// TestArtifactCursorTraversesKindBoundaries verifies kind/type continuation traverses different artifact kinds without omissions or duplicate selections.
func TestArtifactCursorTraversesKindBoundaries(t *testing.T) {
	server := artifactTestServer(artifactFixture())
	const base = "/v1/releases/1.0.0/artifacts?limit=1"
	path := base
	var identities []string
	for pageNumber := 0; ; pageNumber++ {
		require.Less(t, pageNumber, 3, "pagination must terminate")
		response := serveArtifactRequest(server, http.MethodGet, path)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var page v1.Page[v1.ArtifactDescriptor]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
		require.Len(t, page.Items, 1)
		identities = append(identities, string(page.Items[0].Kind)+"/"+page.Items[0].ArtifactType)
		if page.NextCursor == nil {
			break
		}
		path = base + "&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	assert.Equal(t, []string{"context/device", "schema/device", "source-mapping/device-mapping"}, identities)
}

// TestArtifactCursorRejectsChangedBindingsAndMalformedKeysets verifies changed resource parameters and invalid kind/type positions cannot reuse a continuation token.
func TestArtifactCursorRejectsChangedBindingsAndMalformedKeysets(t *testing.T) {
	server := artifactTestServer(artifactFixture())
	response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?limit=1")
	var page v1.Page[v1.ArtifactDescriptor]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.NotNil(t, page.NextCursor)
	for _, path := range []string{
		"/v1/releases/1.0.0/artifacts?limit=2",
		"/v1/releases/2.0.0/artifacts?limit=1",
		"/v1/releases/1.0.0/artifacts?limit=1&kind=schema",
		"/v1/releases/1.0.0/schemas?limit=1",
	} {
		t.Run(path, func(t *testing.T) {
			response := serveArtifactRequest(server, http.MethodGet, path+"&cursor="+url.QueryEscape(*page.NextCursor))
			require.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"invalid-cursor"`)
		})
	}
	for _, last := range []string{"schema", "unknown\x00device", "schema\x00UPPER", "schema\x00device\x00extra"} {
		token, err := pagination.New().Encode(pagination.Payload{
			Resource: "release-artifacts", Limit: 1, Sort: "kind-type", Last: last, Snapshot: "1.0.0",
			Filters: map[string]string{"version": "1.0.0", "kind": "", "artifactType": "", "schemaClass": "", "dependsOnRevision": ""},
		})
		require.NoError(t, err)
		response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?limit=1&cursor="+url.QueryEscape(token))
		require.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"invalid-cursor"`)
	}
}

// TestReleaseArtifactReadsRemainReleaseScopedForConcurrentCallers verifies simultaneous selected metadata reads preserve their release-scoped identity and links.
func TestReleaseArtifactReadsRemainReleaseScopedForConcurrentCallers(t *testing.T) {
	server := artifactTestServer(artifactFixture())
	var group sync.WaitGroup
	results := make(chan struct {
		status         int
		body, selected string
	}, 32)
	for range 32 {
		group.Go(func() {
			response := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts?kind=schema")
			results <- struct {
				status         int
				body, selected string
			}{response.Code, response.Body.String(), response.Header().Get("Selected-Schema-Version")}
		})
	}
	group.Wait()
	close(results)
	var first string
	for result := range results {
		assert.Equal(t, http.StatusOK, result.status)
		assert.Equal(t, "1.0.0", result.selected)
		if first == "" {
			first = result.body
		}
		assert.Equal(t, first, result.body)
	}
}

// TestSelectedArtifactRouteValidatesSelectionAndPath verifies malformed coordinates, unsupported kinds, and absent selections map to appropriate public failures.
func TestSelectedArtifactRouteValidatesSelectionAndPath(t *testing.T) {
	server := artifactTestServer(artifactFixture())
	get := serveArtifactRequest(server, http.MethodGet, "/v1/releases/1.0.0/artifacts/schema/device")
	require.Equal(t, http.StatusOK, get.Code)
	head := serveArtifactRequest(server, http.MethodHead, "/v1/releases/1.0.0/artifacts/schema/device")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Equal(t, get.Header().Get("ETag"), head.Header().Get("ETag"))
	assert.Empty(t, head.Body.String())

	for _, test := range []struct {
		name, path, code string
		status           int
	}{
		{"selected artifact is absent", "/v1/releases/1.0.0/artifacts/schema/unknown", "artifact-not-in-release", http.StatusNotFound},
		{"malformed artifact type", "/v1/releases/1.0.0/artifacts/schema/UPPER", "invalid-request", http.StatusBadRequest},
		{"unsupported artifact kind", "/v1/releases/1.0.0/artifacts/unknown/device", "unsupported-artifact-kind", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, test.status, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
		})
	}
}

// TestImmutableRevisionMetadataSupportsConditionalAndConcurrentReads verifies immutable metadata stays identical across callers and supports bodyless validator revalidation.
func TestImmutableRevisionMetadataSupportsConditionalAndConcurrentReads(t *testing.T) {
	server := artifactTestServer(artifactFixture())
	response := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-schema/metadata")
	require.Equal(t, http.StatusOK, response.Code)
	tag := response.Header().Get("ETag")
	assert.Equal(t, "1.0.0", response.Header().Get("Schema-Version"))
	assert.Empty(t, response.Header().Get("Selected-Schema-Version"))

	conditional := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/rev-schema/metadata", nil)
	request.Header.Set("If-None-Match", tag)
	server.Handler().ServeHTTP(conditional, request)
	assert.Equal(t, http.StatusNotModified, conditional.Code)
	assert.Empty(t, conditional.Body.String())

	head := serveArtifactRequest(server, http.MethodHead, "/v1/artifacts/rev-schema/metadata")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Equal(t, tag, head.Header().Get("ETag"))
	assert.Empty(t, head.Body.String())

	for _, test := range []struct {
		name, path, code string
		status           int
	}{
		{"unknown revision", "/v1/artifacts/unknown/metadata", "artifact-not-found", http.StatusNotFound},
		{"inactive only revision", "/v1/artifacts/inactive/metadata", "artifact-not-found", http.StatusNotFound},
		{"dot segment", "/v1/artifacts/%2E%2E/metadata", "invalid-request", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, test.status, got.Code)
			assert.Contains(t, got.Body.String(), `"code":"`+test.code+`"`)
		})
	}

	// A retired logical identity remains visible when an earlier activated release
	// made its immutable revision reachable.
	retired := fixtureArtifact("rev-retired", "schema", "retired", new("entity-type"), nil, []byte("true"))
	fixture := artifactFixture()
	fixture.artifacts[retired.Revision] = retired
	retiredResponse := serveArtifactRequest(artifactTestServer(fixture), http.MethodGet, "/v1/artifacts/rev-retired/metadata")
	assert.Equal(t, http.StatusOK, retiredResponse.Code)

	var group sync.WaitGroup
	results := make(chan struct {
		status    int
		body, tag string
	}, 32)
	for range 32 {
		group.Go(func() {
			got := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-schema/metadata")
			results <- struct {
				status    int
				body, tag string
			}{got.Code, got.Body.String(), got.Header().Get("ETag")}
		})
	}
	group.Wait()
	close(results)
	var firstBody, firstTag string
	for result := range results {
		assert.Equal(t, http.StatusOK, result.status)
		if firstBody == "" {
			firstBody, firstTag = result.body, result.tag
		}
		assert.Equal(t, firstBody, result.body)
		assert.Equal(t, firstTag, result.tag)
	}
}

// TestArtifactBytesValidateRepresentationsAndFailuresBeforeWriting verifies exact bytes, coding negotiation, HEAD parity, and precommit failures across content routes.
func TestArtifactBytesValidateRepresentationsAndFailuresBeforeWriting(t *testing.T) {
	snapshot := artifactFixture()
	server := artifactTestServer(snapshot)

	// Identity, decoded gzip, and HEAD agree on payload content and integrity, with HEAD retaining its selected representation's validator.
	t.Run("primary identity gzip and HEAD retain integrity metadata", func(t *testing.T) {
		identity := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-schema")
		require.Equal(t, http.StatusOK, identity.Code)
		assert.Equal(t, []byte(`{"type":"object"}`), identity.Body.Bytes())
		assert.Equal(t, snapshot.artifacts["rev-schema"].Payload.Digest, identity.Header().Get("Artifact-Digest"))
		assert.Equal(t, "rev-schema", identity.Header().Get("Artifact-Revision"))
		assert.Equal(t, "application/json", identity.Header().Get("Content-Type"))
		gzipResponse := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/rev-schema", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		server.Handler().ServeHTTP(gzipResponse, request)
		require.Equal(t, http.StatusOK, gzipResponse.Code)
		reader, err := gzip.NewReader(gzipResponse.Body)
		require.NoError(t, err)
		decoded, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		assert.Equal(t, identity.Body.Bytes(), decoded)
		head := serveArtifactRequest(server, http.MethodHead, "/v1/artifacts/rev-schema")
		assert.Equal(t, http.StatusOK, head.Code)
		assert.Equal(t, identity.Header().Get("ETag"), head.Header().Get("ETag"))
		assert.Empty(t, head.Body.String())
	})

	// Empty and one-byte resources retain exact content lengths; gzip decoding preserves the one-byte resource.
	t.Run("named resources preserve empty and one byte values", func(t *testing.T) {
		empty := fixtureBytes(nil, "application/octet-stream")
		one := fixtureBytes([]byte("x"), "application/octet-stream")
		snapshot.resources["rev-schema"] = append(snapshot.resources["rev-schema"],
			model.Resource{ArtifactRevision: "rev-schema", Name: "empty", Role: "auxiliary", ByteDescriptor: empty},
			model.Resource{ArtifactRevision: "rev-schema", Name: "one", Role: "auxiliary", ByteDescriptor: one},
		)
		for _, test := range []struct {
			path     string
			expected []byte
		}{
			{"/v1/artifacts/rev-schema/resources/empty", []byte{}},
			{"/v1/artifacts/rev-schema/resources/one", []byte("x")},
		} {
			got := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, http.StatusOK, got.Code)
			assert.Len(t, got.Body.Bytes(), len(test.expected))
			if len(test.expected) > 0 {
				assert.Equal(t, test.expected, got.Body.Bytes())
			}
		}
		compressed := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/artifacts/rev-schema/resources/one", nil)
		request.Header.Set("Accept-Encoding", "gzip")
		server.Handler().ServeHTTP(compressed, request)
		require.Equal(t, http.StatusOK, compressed.Code)
		reader, err := gzip.NewReader(compressed.Body)
		require.NoError(t, err)
		decoded, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		assert.Equal(t, []byte("x"), decoded)
	})

	// Exact-limit stored payloads remain retrievable without truncation at the 16 MiB artifact boundary.
	t.Run("exact sixteen MiB boundary remains servable", func(t *testing.T) {
		bytes := make([]byte, 16*1024*1024)
		bytes[0] = 'x'
		boundary := fixtureArtifact("rev-boundary", "schema", "boundary", new("entity-type"), nil, bytes)
		snapshot.artifacts[boundary.Revision] = boundary
		got := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-boundary")
		assert.Equal(t, http.StatusOK, got.Code)
		assert.Len(t, got.Body.Bytes(), len(bytes))
	})

	for _, test := range []struct {
		name, path, code string
		configure        func()
	}{
		{"inactive primary", "/v1/artifacts/inactive", "artifact-not-found", func() {}},
		{"missing resource", "/v1/artifacts/rev-schema/resources/missing", "resource-not-found", func() {}},
		{"unsafe resource", "/v1/artifacts/rev-schema/resources/%2E%2E", "invalid-request", func() {}},
		{"over profile and corrupt primary", "/v1/artifacts/rev-schema", "inconsistent-registry-state", func() { snapshot.artifactErr = postgres.ErrInconsistentState }},
		{"corrupt resource", "/v1/artifacts/rev-schema/resources/example", "inconsistent-registry-state", func() { snapshot.resourceErr = postgres.ErrInconsistentState }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot.artifactErr, snapshot.resourceErr = nil, nil
			test.configure()
			got := serveArtifactRequest(server, http.MethodGet, test.path)
			switch test.code {
			case "invalid-request":
				assert.Equal(t, http.StatusBadRequest, got.Code)
			case "inconsistent-registry-state":
				assert.Equal(t, http.StatusInternalServerError, got.Code)
			default:
				assert.Equal(t, http.StatusNotFound, got.Code)
			}
			assert.Contains(t, got.Body.String(), `"code":"`+test.code+`"`)
			assert.NotEqual(t, snapshot.artifacts["rev-schema"].Payload.Bytes, got.Body.Bytes(), "failed exact-byte requests must not return corrupt data")
		})
	}
}

// TestSimultaneousExactByteRequestsAreIdentical verifies concurrent immutable content requests return identical bytes and integrity metadata.
func TestSimultaneousExactByteRequestsAreIdentical(t *testing.T) {
	snapshot := artifactFixture()
	server := artifactTestServer(snapshot)
	var group sync.WaitGroup
	results := make(chan struct {
		status                       int
		body, mediaType, digest, tag string
	}, 32)
	for range 32 {
		group.Go(func() {
			got := serveArtifactRequest(server, http.MethodGet, "/v1/artifacts/rev-schema")
			results <- struct {
				status                       int
				body, mediaType, digest, tag string
			}{got.Code, got.Body.String(), got.Header().Get("Content-Type"), got.Header().Get("Artifact-Digest"), got.Header().Get("ETag")}
		})
	}
	group.Wait()
	close(results)
	var first struct {
		status                       int
		body, mediaType, digest, tag string
	}
	for result := range results {
		assert.Equal(t, http.StatusOK, result.status)
		if first.tag == "" {
			first = result
		}
		assert.Equal(t, first.body, result.body)
		assert.Equal(t, first.mediaType, result.mediaType)
		assert.Equal(t, first.digest, result.digest)
		assert.Equal(t, first.tag, result.tag)
	}
}

// TestCanonicalResourcePathsRemainImmutable verifies canonical coordinates identify exact immutable content and mismatched identity coordinates fail.
func TestCanonicalResourcePathsRemainImmutable(t *testing.T) {
	snapshot := artifactFixture()
	server := artifactTestServer(snapshot)
	path := "/v1/resources/schema/device/rev-schema"
	identity := serveArtifactRequest(server, http.MethodGet, path)
	require.Equal(t, http.StatusOK, identity.Code)
	assert.Equal(t, snapshot.artifacts["rev-schema"].Payload.Bytes, identity.Body.Bytes())
	assert.Equal(t, snapshot.artifacts["rev-schema"].Payload.Digest, identity.Header().Get("Artifact-Digest"))

	head := serveArtifactRequest(server, http.MethodHead, path)
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String())
	gzipResponse := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Accept-Encoding", "gzip")
	server.Handler().ServeHTTP(gzipResponse, request)
	require.Equal(t, http.StatusOK, gzipResponse.Code)
	reader, err := gzip.NewReader(gzipResponse.Body)
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, identity.Body.Bytes(), decoded)

	for _, test := range []struct {
		name, path, code string
		status           int
	}{
		{"kind mismatch", "/v1/resources/context/device/rev-schema", "artifact-not-found", http.StatusNotFound},
		{"type mismatch", "/v1/resources/schema/other/rev-schema", "artifact-not-found", http.StatusNotFound},
		{"unknown revision", "/v1/resources/schema/device/unknown", "artifact-not-found", http.StatusNotFound},
		{"inactive only", "/v1/resources/schema/device/inactive", "artifact-not-found", http.StatusNotFound},
		{"unsupported kind", "/v1/resources/unknown/device/rev-schema", "unsupported-artifact-kind", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, test.status, got.Code)
			assert.Contains(t, got.Body.String(), `"code":"`+test.code+`"`)
		})
	}

	// A later externally supplied current release changes only the response
	// snapshot header. The deployment-lifetime canonical bytes remain unchanged.
	snapshot.current = model.CurrentState{SchemaVersion: "1.1.0", ActivationSequence: 2, SnapshotSequence: 2}
	snapshot.releases["1.1.0"] = model.Release{SchemaVersion: "1.1.0", ActivationSequence: 2}
	historical := serveArtifactRequest(server, http.MethodGet, path)
	require.Equal(t, http.StatusOK, historical.Code)
	assert.Equal(t, identity.Body.Bytes(), historical.Body.Bytes())
	assert.Equal(t, "1.1.0", historical.Header().Get("Schema-Version"))
}

// TestReleaseScopedURIResolverUsesExactSafeRedirects verifies exact catalog lookup produces conditional, server-owned redirects unaffected by request authority.
func TestReleaseScopedURIResolverUsesExactSafeRedirects(t *testing.T) {
	snapshot := artifactFixture()
	payloadURI := "https://catalog.example.test/device"
	resourceURI := "https://catalog.example.test/program"
	escapedResourceURI := "https://catalog.example.test/escaped-program"
	unicodeURI := "https://catalog.example.test/é"
	hostileURI := "https://attacker.example.test/%0d%0aLocation:%20https://attacker.example.test/"
	snapshot.uris = map[string]model.URIEntry{
		"1.0.0\x00" + payloadURI:         {SchemaVersion: "1.0.0", URI: payloadURI, ArtifactRevision: "rev-schema"},
		"1.0.0\x00" + resourceURI:        {SchemaVersion: "1.0.0", URI: resourceURI, ArtifactRevision: "rev-schema", ResourceName: new("example")},
		"1.0.0\x00" + escapedResourceURI: {SchemaVersion: "1.0.0", URI: escapedResourceURI, ArtifactRevision: "rev-schema", ResourceName: new("program name")},
		"1.0.0\x00" + unicodeURI:         {SchemaVersion: "1.0.0", URI: unicodeURI, ArtifactRevision: "rev-context"},
		"1.0.0\x00" + hostileURI:         {SchemaVersion: "1.0.0", URI: hostileURI, ArtifactRevision: "rev-schema"},
	}
	server := artifactTestServer(snapshot)

	requestResolver := func(method, uri string) *httptest.ResponseRecorder {
		return serveArtifactRequest(server, method, "/v1/releases/1.0.0/resolve?uri="+url.QueryEscape(uri))
	}

	payload := requestResolver(http.MethodGet, payloadURI)
	require.Equal(t, http.StatusTemporaryRedirect, payload.Code)
	assert.Equal(t, "/v1/artifacts/rev-schema", payload.Header().Get("Location"))
	assert.Equal(t, "1.0.0", payload.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", payload.Header().Get("Selected-Schema-Version"))
	assert.NotEmpty(t, payload.Header().Get("ETag"))

	resource := requestResolver(http.MethodGet, resourceURI)
	require.Equal(t, http.StatusTemporaryRedirect, resource.Code)
	assert.Equal(t, "/v1/artifacts/rev-schema/resources/example", resource.Header().Get("Location"))
	escapedResource := requestResolver(http.MethodGet, escapedResourceURI)
	assert.Equal(t, http.StatusTemporaryRedirect, escapedResource.Code)
	assert.Equal(t, "/v1/artifacts/rev-schema/resources/program%20name", escapedResource.Header().Get("Location"))

	head := requestResolver(http.MethodHead, payloadURI)
	assert.Equal(t, http.StatusTemporaryRedirect, head.Code)
	assert.Empty(t, head.Body.String())

	conditionalRequest := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/resolve?uri="+url.QueryEscape(payloadURI), nil)
	conditionalRequest.Header.Set("If-None-Match", payload.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	server.Handler().ServeHTTP(conditional, conditionalRequest)
	assert.Equal(t, http.StatusNotModified, conditional.Code)
	assert.Empty(t, conditional.Header().Get("Location"))

	unicode := requestResolver(http.MethodGet, unicodeURI)
	assert.Equal(t, http.StatusTemporaryRedirect, unicode.Code)
	caseMismatch := requestResolver(http.MethodGet, "https://catalog.example.test/É")
	assert.Equal(t, http.StatusNotFound, caseMismatch.Code)

	hostile := requestResolver(http.MethodGet, hostileURI)
	require.Equal(t, http.StatusTemporaryRedirect, hostile.Code)
	assert.Equal(t, "/v1/artifacts/rev-schema", hostile.Header().Get("Location"))
	hostRequest := httptest.NewRequest(http.MethodGet, "/v1/releases/1.0.0/resolve?uri="+url.QueryEscape(payloadURI), nil)
	hostRequest.Host = "hostile.example.test"
	hostRequest.Header.Set("X-Forwarded-Host", "also-hostile.example.test")
	hostResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(hostResponse, hostRequest)
	assert.Equal(t, "/v1/artifacts/rev-schema", hostResponse.Header().Get("Location"))

	// The explicit release remains selected even when an externally supplied
	// current pointer changes before the next request.
	snapshot.current = model.CurrentState{SchemaVersion: "1.1.0", ActivationSequence: 2, SnapshotSequence: 2}
	snapshot.releases["1.1.0"] = model.Release{SchemaVersion: "1.1.0", ActivationSequence: 2}
	historical := requestResolver(http.MethodGet, payloadURI)
	assert.Equal(t, http.StatusTemporaryRedirect, historical.Code)
	assert.Equal(t, "/v1/artifacts/rev-schema", historical.Header().Get("Location"))
	assert.Equal(t, "1.1.0", historical.Header().Get("Schema-Version"))
	assert.Equal(t, "1.0.0", historical.Header().Get("Selected-Schema-Version"))
}

// TestReleaseScopedURIResolverValidatesQueryBoundaries verifies malformed, absent, duplicate, and unsupported URI query parameters fail before resolution.
func TestReleaseScopedURIResolverValidatesQueryBoundaries(t *testing.T) {
	snapshot := artifactFixture()
	exactURI := "https://catalog.example.test/" + strings.Repeat("a", 8192-len("https://catalog.example.test/"))
	snapshot.uris = map[string]model.URIEntry{"1.0.0\x00" + exactURI: {SchemaVersion: "1.0.0", URI: exactURI, ArtifactRevision: "rev-schema"}}
	server := artifactTestServer(snapshot)

	request := func(path string) *httptest.ResponseRecorder {
		return serveArtifactRequest(server, http.MethodGet, path)
	}
	exact := request("/v1/releases/1.0.0/resolve?uri=" + url.QueryEscape(exactURI))
	assert.Equal(t, http.StatusTemporaryRedirect, exact.Code)
	for _, test := range []struct{ name, path, code string }{
		{"missing", "/v1/releases/1.0.0/resolve", "invalid-query-parameter"},
		{"empty", "/v1/releases/1.0.0/resolve?uri=", "invalid-query-parameter"},
		{"duplicate", "/v1/releases/1.0.0/resolve?uri=one&uri=two", "invalid-query-parameter"},
		{"oversized", "/v1/releases/1.0.0/resolve?uri=" + url.QueryEscape(exactURI+"x"), "invalid-query-parameter"},
		{"absent exact key", "/v1/releases/1.0.0/resolve?uri=" + url.QueryEscape("https://catalog.example.test/missing"), "uri-not-in-release"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(test.path)
			if test.code == "uri-not-in-release" {
				assert.Equal(t, http.StatusNotFound, response.Code)
			} else {
				assert.Equal(t, http.StatusBadRequest, response.Code)
			}
			assert.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
		})
	}
}

// TestLogicalArtifactHistoryPaginatesWithinItsInitialSnapshot verifies continuation excludes later activations while preserving historical selection metadata and links.
func TestLogicalArtifactHistoryPaginatesWithinItsInitialSnapshot(t *testing.T) {
	snapshot := artifactFixture()
	old := fixtureArtifact("rev-old", "schema", "device", new("entity-type"), nil, []byte("false"))
	snapshot.artifacts[old.Revision] = old
	snapshot.history = []model.ReleaseArtifact{
		{SchemaVersion: "1.0.2", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 3},
		{SchemaVersion: "1.0.1", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 2},
		{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-old", ActivationSequence: 1},
	}
	snapshot.current.SnapshotSequence = 3
	server := artifactTestServer(snapshot)

	response := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1")
	require.Equal(t, http.StatusOK, response.Code)
	var first v1.Page[v1.ArtifactHistoryItem]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
	require.Len(t, first.Items, 1)
	require.NotNil(t, first.NextCursor)
	assert.Equal(t, "rev-schema", first.Items[0].ArtifactRevision)
	assert.Equal(t, "/v1/releases/1.0.2/artifacts/schema/device", first.Items[0].Links["artifact"])
	assert.Equal(t, "/v1/artifacts/rev-schema/metadata", first.Items[0].Links["metadata"])

	// Externally supplied newer history must not appear when a cursor resumes.
	snapshot.history = append([]model.ReleaseArtifact{{SchemaVersion: "1.0.3", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 4}}, snapshot.history...)
	snapshot.current = model.CurrentState{SchemaVersion: "1.0.3", ActivationSequence: 4, SnapshotSequence: 4}
	snapshot.releases["1.0.3"] = model.Release{SchemaVersion: "1.0.3", ActivationSequence: 4}
	second := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1&cursor="+url.QueryEscape(*first.NextCursor))
	require.Equal(t, http.StatusOK, second.Code)
	var secondBody v1.Page[v1.ArtifactHistoryItem]
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondBody))
	require.Len(t, secondBody.Items, 1)
	assert.Equal(t, "1.0.1", secondBody.Items[0].SchemaVersion)
	assert.Equal(t, "rev-schema", secondBody.Items[0].ArtifactRevision)
}

// TestLogicalArtifactHistoryValidationAndPageShapes verifies logical identity validation, absent history classification, page limits, and complete envelope shapes.
func TestLogicalArtifactHistoryValidationAndPageShapes(t *testing.T) {
	// An identity without activated selections returns logical-artifact-not-found rather than a successful empty history page.
	t.Run("no history is not found", func(t *testing.T) {
		response := serveArtifactRequest(artifactTestServer(artifactFixture()), http.MethodGet, "/v1/artifact-kinds/schema/device/history")
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"logical-artifact-not-found"`)
	})

	// Single selections need no continuation; maximum-length history pages retain a cursor and support bodyless HEAD.
	t.Run("single and maximum pages", func(t *testing.T) {
		snapshot := artifactFixture()
		snapshot.history = []model.ReleaseArtifact{{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 1}}
		server := artifactTestServer(snapshot)
		single := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history")
		assert.Equal(t, http.StatusOK, single.Code)
		assert.Contains(t, single.Body.String(), `"nextCursor":null`)
		head := serveArtifactRequest(server, http.MethodHead, "/v1/artifact-kinds/schema/device/history")
		assert.Equal(t, http.StatusOK, head.Code)
		assert.Empty(t, head.Body.String())

		items := make([]model.ReleaseArtifact, 1001)
		for index := range items {
			revision := "history-" + fmt.Sprintf("%04d", index)
			entityArtifact := fixtureArtifact(revision, "schema", "device", new("entity-type"), nil, []byte("true"))
			snapshot.artifacts[revision] = entityArtifact
			items[index] = model.ReleaseArtifact{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: revision, ActivationSequence: int64(1001 - index)}
		}
		snapshot.history = items
		snapshot.current.SnapshotSequence = 1001
		maximum := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1000")
		require.Equal(t, http.StatusOK, maximum.Code)
		var body v1.Page[v1.ArtifactHistoryItem]
		require.NoError(t, json.Unmarshal(maximum.Body.Bytes(), &body))
		assert.Len(t, body.Items, 1000)
		assert.NotNil(t, body.NextCursor)
	})

	server := artifactTestServer(artifactFixture())
	for _, test := range []struct {
		name, path, code string
		status           int
	}{
		{"unknown filter", "/v1/artifact-kinds/schema/device/history?kind=schema", "invalid-query-parameter", http.StatusBadRequest},
		{"unsupported kind", "/v1/artifact-kinds/unknown/device/history", "unsupported-artifact-kind", http.StatusBadRequest},
		{"malformed type", "/v1/artifact-kinds/schema/UPPER/history", "invalid-request", http.StatusBadRequest},
		{"invalid limit", "/v1/artifact-kinds/schema/device/history?limit=0", "invalid-query-parameter", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveArtifactRequest(server, http.MethodGet, test.path)
			assert.Equal(t, test.status, response.Code)
			assert.Contains(t, response.Body.String(), `"code":"`+test.code+`"`)
		})
	}

	// A valid stateless history continuation succeeds without emitting cursor-expired.
	t.Run("cursor remains valid without expiry", func(t *testing.T) {
		snapshot := artifactFixture()
		snapshot.current = model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 2}
		snapshot.releases["2.0.0"] = model.Release{SchemaVersion: "2.0.0", ActivationSequence: 2}
		snapshot.history = []model.ReleaseArtifact{
			{SchemaVersion: "2.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 2},
			{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 1},
		}
		server := artifactTestServer(snapshot)
		first := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1")
		require.Equal(t, http.StatusOK, first.Code)
		var page v1.Page[v1.ArtifactHistoryItem]
		require.NoError(t, json.Unmarshal(first.Body.Bytes(), &page))
		require.NotNil(t, page.NextCursor)
		response := serveArtifactRequest(server, http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1&cursor="+url.QueryEscape(*page.NextCursor))
		assert.Equal(t, http.StatusOK, response.Code)
		assert.NotContains(t, response.Body.String(), `"cursor-expired"`)
	})
}

// TestHistoryCursorRejectsInvalidSequences verifies unsigned history cursors still require positive numeric continuation and snapshot coordinates.
func TestHistoryCursorRejectsInvalidSequences(t *testing.T) {
	for _, coordinate := range []string{"last", "snapshot"} {
		for _, invalid := range []string{"text", "0", "-1", "9223372036854775808"} {
			t.Run(coordinate+"/"+invalid, func(t *testing.T) {
				payload := pagination.Payload{Resource: "artifact-history", Limit: 1, Sort: "activation-desc", Last: "2", Snapshot: "3", Filters: map[string]string{"kind": "schema", "artifactType": "device"}}
				if coordinate == "last" {
					payload.Last = invalid
				} else {
					payload.Snapshot = invalid
				}
				token, err := pagination.New().Encode(payload)
				require.NoError(t, err)
				response := serveArtifactRequest(artifactTestServer(artifactFixture()), http.MethodGet, "/v1/artifact-kinds/schema/device/history?limit=1&cursor="+url.QueryEscape(token))
				require.Equal(t, http.StatusBadRequest, response.Code)
				assert.Contains(t, response.Body.String(), `"code":"invalid-cursor"`)
			})
		}
	}
}

// TestHistoryRejectsLateDescriptorFailureWithoutPartialPage verifies a later item's metadata failure rejects the whole page without publishing earlier items.
func TestHistoryRejectsLateDescriptorFailureWithoutPartialPage(t *testing.T) {
	snapshot := artifactFixture()
	snapshot.current.SnapshotSequence = 2
	snapshot.history = []model.ReleaseArtifact{
		{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "rev-schema", ActivationSequence: 2},
		{SchemaVersion: "1.0.0", Kind: "schema", ArtifactType: "device", ArtifactRevision: "missing-revision", ActivationSequence: 1},
	}
	response := serveArtifactRequest(artifactTestServer(snapshot), http.MethodGet, "/v1/artifact-kinds/schema/device/history")
	require.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"logical-artifact-not-found"`)
	assert.NotContains(t, response.Body.String(), `"items"`)
	assert.NotContains(t, response.Body.String(), "rev-schema")
}

// artifactFixture creates visible release selections spanning schemas, contexts, mappings, resources, dependencies, and historical revision reuse.
func artifactFixture() *artifactTestSnapshot {
	class, target := "entity-type", "1.0.0"
	schemaArtifact := fixtureArtifact("rev-schema", "schema", "device", &class, nil, []byte(`{"type":"object"}`))
	schemaArtifact.Extensions = []byte(`{"org.example.review":{"approved":true}}`)
	contextArtifact := fixtureArtifact("rev-context", "context", "device", nil, nil, []byte(`{"@context":{}}`))
	sourceMappingArtifact := fixtureArtifact("rev-mapping", "source-mapping", "device-mapping", nil, &target, []byte(`{"mapping":true}`))
	return &artifactTestSnapshot{
		current:      model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1},
		releases:     map[string]model.Release{"1.0.0": {SchemaVersion: "1.0.0", ActivationSequence: 1}},
		artifacts:    map[string]model.Artifact{"rev-schema": schemaArtifact, "rev-context": contextArtifact, "rev-mapping": sourceMappingArtifact},
		selections:   map[string]model.Artifact{"schema/device": schemaArtifact, "context/device": contextArtifact, "source-mapping/device-mapping": sourceMappingArtifact},
		collections:  map[string][]model.Artifact{"1.0.0": {schemaArtifact, contextArtifact, sourceMappingArtifact}},
		history:      []model.ReleaseArtifact{},
		resources:    map[string][]model.Resource{"rev-schema": {{ArtifactRevision: "rev-schema", Name: "example", Role: "example", ByteDescriptor: fixtureBytes([]byte("resource"), "text/plain")}}, "rev-context": {}, "rev-mapping": {}},
		dependencies: map[string][]model.Dependency{"rev-schema": {{ArtifactRevision: "rev-schema", Kind: "context", ArtifactType: "device", Revision: "rev-context"}}, "rev-context": {}, "rev-mapping": {}},
		mappings:     map[string]model.SourceMappingDetail{"rev-mapping": {SchemaVersion: "1.0.0", MappingRevision: "rev-mapping", SourceArtifactType: "device"}},
		uris:         map[string]model.URIEntry{},
		bindings:     map[string][]model.SchemaContextBinding{"1.0.0\x00rev-schema": {{SchemaVersion: "1.0.0", SchemaRevision: "rev-schema", ContextRevision: "rev-context"}}},
		stores:       map[string][]model.StorageEntry{"1.0.0\x00device": {{SchemaVersion: "1.0.0", ArtifactType: "device", Store: "data-center-inventory"}}},
	}
}

// fixtureArtifact supplies consistent immutable identity, optional classification, and exact payload integrity metadata.
func fixtureArtifact(revision, kind, artifactType string, schemaClass, target *string, bytes []byte) model.Artifact {
	return model.Artifact{Revision: revision, Kind: kind, Type: artifactType, CreatedUnderSchemaVersion: "1.0.0", SchemaClass: schemaClass, TargetSchemaVersion: target, CanonicalURI: "https://registry.example.test/v1/resources/" + kind + "/" + artifactType + "/" + revision, Payload: fixtureBytes(bytes, "application/json")}
}

// fixtureBytes supplies exact-byte size and digest metadata for route fixture content.
func fixtureBytes(bytes []byte, mediaType string) model.ByteDescriptor {
	sum := sha256.Sum256(bytes)
	return model.ByteDescriptor{MediaType: mediaType, Digest: "sha-256:" + hex.EncodeToString(sum[:]), Size: int64(len(bytes)), Bytes: bytes}
}

// artifactTestServer wires a controlled snapshot into both artifact and release services for complete in-process routing.
func artifactTestServer(snapshot *artifactTestSnapshot) *Server {
	cfg := testConfig()
	cfg.Admission.MaxInFlight = 64
	reader := artifactTestReader{snapshot: snapshot}
	return NewWithServices(cfg, &fakeChecker{}, release.New(reader), artifact.New(reader))
}

// serveArtifactRequest captures an in-process response for status, header, and complete-body assertions.
func serveArtifactRequest(server *Server, method, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response
}
