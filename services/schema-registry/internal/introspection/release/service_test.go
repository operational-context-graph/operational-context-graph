package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

// testReader injects a controlled snapshot or a read-boundary failure without a database.
type testReader struct {
	snapshot *testSnapshot
	err      error
}

// Read invokes the callback with fixture state unless the configured boundary failure prevents it.
func (r testReader) Read(ctx context.Context, fn func(context.Context, Snapshot) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(ctx, r.snapshot)
}

// testSnapshot supplies release, lineage, and artifact evidence and records pagination budgets for service assertions.
type testSnapshot struct {
	current     model.CurrentState
	releases    map[string]model.Release
	calls       []int64
	limits      []int
	history     []model.Release
	ancestor    *bool
	lineageErr  error
	artifacts   map[string]model.Artifact
	currentErr  error
	historyErr  error
	artifactErr error
}

// CurrentState supplies the controlled current pointer and paging ceiling.
func (s *testSnapshot) CurrentState() model.CurrentState { return s.current }

// CurrentRelease resolves the fixture pointer or injects a current-release failure.
func (s *testSnapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	if s.currentErr != nil {
		return model.Release{}, s.currentErr
	}
	return s.Release(ctx, s.current.SchemaVersion)
}

// Release returns a fixture release or ErrNotFound for an absent version.
func (s *testSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	value, ok := s.releases[version]
	if !ok {
		return model.Release{}, postgres.ErrNotFound
	}
	return value, nil
}

// ReleasesAt records the requested ceiling and limit and returns configured history or its injected failure.
func (s *testSnapshot) ReleasesAt(_ context.Context, before *int64, maximum int64, limit int) ([]model.Release, error) {
	s.calls = append(s.calls, maximum)
	s.limits = append(s.limits, limit)
	return s.history, s.historyErr
}

// IsAncestor supplies controlled endpoint ancestry, defaulting to a connected chain when no override exists.
func (s *testSnapshot) IsAncestor(context.Context, string, string) (bool, error) {
	if s.lineageErr != nil {
		return false, s.lineageErr
	}
	if s.ancestor == nil {
		return true, nil
	}
	return *s.ancestor, nil
}

// Artifact returns endpoint compatibility bytes or injects a missing-revision or storage failure.
func (s *testSnapshot) Artifact(_ context.Context, revision string) (model.Artifact, error) {
	if s.artifactErr != nil {
		return model.Artifact{}, s.artifactErr
	}
	if value, ok := s.artifacts[revision]; ok {
		return value, nil
	}
	return model.Artifact{}, postgres.ErrNotFound
}

// ReleaseArtifact reports no selection; release-only fixtures do not support artifact selection routes.
func (*testSnapshot) ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error) {
	return model.Artifact{}, postgres.ErrNotFound
}

// ReleaseArtifacts reports an absent collection outside this release-only fixture's scope.
func (*testSnapshot) ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error) {
	return nil, postgres.ErrNotFound
}

// ArtifactHistory reports absent logical history outside the supported release operations.
func (*testSnapshot) ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error) {
	return nil, postgres.ErrNotFound
}

// Resources reports absent resource metadata in release-only fixtures.
func (*testSnapshot) Resources(context.Context, string) ([]model.Resource, error) {
	return nil, postgres.ErrNotFound
}

// Resource reports absent named content in release-only fixtures.
func (*testSnapshot) Resource(context.Context, string, string) (model.Resource, error) {
	return model.Resource{}, postgres.ErrNotFound
}

// Dependencies reports absent projected dependencies outside endpoint comparison evidence.
func (*testSnapshot) Dependencies(context.Context, string) ([]model.Dependency, error) {
	return nil, postgres.ErrNotFound
}

// ContextBindings reports absent context selections in release-only fixtures.
func (*testSnapshot) ContextBindings(context.Context, string, string) ([]model.SchemaContextBinding, error) {
	return nil, postgres.ErrNotFound
}

// StorageEntries reports absent storage projections in release-only fixtures.
func (*testSnapshot) StorageEntries(context.Context, string, string) ([]model.StorageEntry, error) {
	return nil, postgres.ErrNotFound
}

// URIEntry reports absent URI projections in release-only fixtures.
func (*testSnapshot) URIEntry(context.Context, string, string) (model.URIEntry, error) {
	return model.URIEntry{}, postgres.ErrNotFound
}

// SourceMapping reports absent logical mapping projections in release-only fixtures.
func (*testSnapshot) SourceMapping(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// SourceMappingByRevision reports absent revision mapping projections in release-only fixtures.
func (*testSnapshot) SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

// TestListBindsCallerCursorToOriginalSnapshot verifies continuation uses the caller's original activation ceiling instead of the newer current ceiling.
func TestListBindsCallerCursorToOriginalSnapshot(t *testing.T) {
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 9}, releases: map[string]model.Release{"2.0.0": {SchemaVersion: "2.0.0"}}}
	requested := int64(5)
	page, err := New(testReader{snapshot: snapshot}).List(context.Background(), nil, &requested, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 5, page.Current.SnapshotSequence)
	require.Equal(t, []int64{5}, snapshot.calls)
}

// TestCompareRejectsEqualAndReversedVersionsBeforeParsing verifies invalid endpoint order returns ErrVersionsNotComparable before missing manifest bytes matter.
func TestCompareRejectsEqualAndReversedVersionsBeforeParsing(t *testing.T) {
	first := model.Release{SchemaVersion: "1.0.0", ActivationSequence: 1}
	second := model.Release{SchemaVersion: "2.0.0", ActivationSequence: 2}
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "2.0.0"}, releases: map[string]model.Release{"1.0.0": first, "2.0.0": second}}
	service := New(testReader{snapshot: snapshot})
	_, _, err := service.Compare(context.Background(), "1.0.0", "1.0.0")
	require.ErrorIs(t, err, ErrVersionsNotComparable)
	_, _, err = service.Compare(context.Background(), "2.0.0", "1.0.0")
	require.ErrorIs(t, err, ErrVersionsNotComparable)
}

// TestCompareRejectsDisconnectedEndpointsWithAscendingSequences verifies increasing activation numbers alone do not establish comparable ancestry.
func TestCompareRejectsDisconnectedEndpointsWithAscendingSequences(t *testing.T) {
	ancestor := false
	first := model.Release{SchemaVersion: "1.0.0", ActivationSequence: 1}
	second := model.Release{SchemaVersion: "2.0.0", ActivationSequence: 2}
	snapshot := &testSnapshot{
		current:  model.CurrentState{SchemaVersion: second.SchemaVersion, ActivationSequence: second.ActivationSequence},
		releases: map[string]model.Release{first.SchemaVersion: first, second.SchemaVersion: second},
		ancestor: &ancestor,
	}

	_, _, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), first.SchemaVersion, second.SchemaVersion)
	require.ErrorIs(t, err, ErrVersionsNotComparable)
}

// TestCompareRejectsInconsistentEndpointLineage verifies invalid ancestry evidence remains an integrity failure rather than ordinary noncomparability.
func TestCompareRejectsInconsistentEndpointLineage(t *testing.T) {
	first := model.Release{SchemaVersion: "1.0.0", ActivationSequence: 1}
	second := model.Release{SchemaVersion: "2.0.0", ActivationSequence: 2}
	snapshot := &testSnapshot{
		current:    model.CurrentState{SchemaVersion: second.SchemaVersion, ActivationSequence: second.ActivationSequence},
		releases:   map[string]model.Release{first.SchemaVersion: first, second.SchemaVersion: second},
		lineageErr: postgres.ErrInconsistentState,
	}

	_, _, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), first.SchemaVersion, second.SchemaVersion)
	require.ErrorIs(t, err, postgres.ErrInconsistentState)
}

// TestComparePropagatesMissingReleaseAndStorageFailure verifies absent endpoints and unavailable read boundaries fail comparison.
func TestComparePropagatesMissingReleaseAndStorageFailure(t *testing.T) {
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0"}, releases: map[string]model.Release{"1.0.0": {SchemaVersion: "1.0.0"}}}
	_, _, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), "1.0.0", "2.0.0")
	require.ErrorIs(t, err, postgres.ErrNotFound)
	_, _, err = New(testReader{err: errors.New("database unavailable")}).Compare(context.Background(), "1.0.0", "2.0.0")
	require.Error(t, err)
}

// TestCompareAdaptsVerifiedEndpointArtifacts verifies endpoint schema bytes identify a breaking update without listing intermediate release history.
func TestCompareAdaptsVerifiedEndpointArtifacts(t *testing.T) {
	first := comparisonManifest(t, "1.0.0", "rev-device-v1", nil)
	second := comparisonManifest(t, "1.0.1", "rev-device-v2", first)
	before := model.Release{SchemaVersion: "1.0.0", ManifestDigest: manifestDigest(first), ManifestBytes: first, ActivationSequence: 1}
	after := model.Release{SchemaVersion: "1.0.1", ManifestDigest: manifestDigest(second), ManifestBytes: second, ActivationSequence: 2}
	snapshot := &testSnapshot{
		current:  model.CurrentState{SchemaVersion: after.SchemaVersion, ActivationSequence: 2, SnapshotSequence: 2},
		releases: map[string]model.Release{before.SchemaVersion: before, after.SchemaVersion: after},
		history:  []model.Release{after, before},
		artifacts: map[string]model.Artifact{
			"rev-device-v1": {Revision: "rev-device-v1", Payload: model.ByteDescriptor{MediaType: artifact.SchemaMediaType, Bytes: []byte(`{"type":"object","minimum":1}`)}},
			"rev-device-v2": {Revision: "rev-device-v2", Payload: model.ByteDescriptor{MediaType: artifact.SchemaMediaType, Bytes: []byte(`{"type":"object","minimum":2}`)}},
		},
	}

	changes, current, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), before.SchemaVersion, after.SchemaVersion)
	require.NoError(t, err)
	assert.Equal(t, after.SchemaVersion, current.SchemaVersion)
	assert.Empty(t, snapshot.calls)
	assert.Empty(t, snapshot.limits)
	require.Len(t, changes.Artifacts, 1)
	assert.Equal(t, manifest.OperationUpdate, changes.Artifacts[0].Operation)
	assert.Equal(t, "rev-device-v1", changes.Artifacts[0].BeforeRevision)
	assert.Equal(t, "rev-device-v2", changes.Artifacts[0].AfterRevision)
	assert.Equal(t, manifest.ImpactBreaking, changes.Artifacts[0].Impact)
	assert.Equal(t, manifest.CompatibilityBreaking, changes.Compatibility)
}

// TestCompareUsesIndexedEndpointOrderForDistantReleases verifies distant connected endpoints compare without history queries or pagination limits.
func TestCompareUsesIndexedEndpointOrderForDistantReleases(t *testing.T) {
	first := comparisonManifest(t, "1.0.0", "rev-device-v1", nil)
	second := comparisonManifest(t, "1.0.1", "rev-device-v2", first)
	before := model.Release{SchemaVersion: "1.0.0", ManifestDigest: manifestDigest(first), ManifestBytes: first, ActivationSequence: 1}
	after := model.Release{SchemaVersion: "1.0.1", ManifestDigest: manifestDigest(second), ManifestBytes: second, ActivationSequence: 10002}
	snapshot := &testSnapshot{
		current:  model.CurrentState{SchemaVersion: after.SchemaVersion, ActivationSequence: after.ActivationSequence, SnapshotSequence: after.ActivationSequence},
		releases: map[string]model.Release{before.SchemaVersion: before, after.SchemaVersion: after},
		artifacts: map[string]model.Artifact{
			"rev-device-v1": {Revision: "rev-device-v1", Payload: model.ByteDescriptor{MediaType: artifact.SchemaMediaType, Bytes: []byte(`{"type":"object","minimum":1}`)}},
			"rev-device-v2": {Revision: "rev-device-v2", Payload: model.ByteDescriptor{MediaType: artifact.SchemaMediaType, Bytes: []byte(`{"type":"object","minimum":2}`)}},
		},
	}

	_, _, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), before.SchemaVersion, after.SchemaVersion)
	require.NoError(t, err)
	assert.Empty(t, snapshot.calls)
	assert.Empty(t, snapshot.limits)
}

// TestGetPreservesCurrentVersionForUnknownRelease verifies unknown selections retain resolved current metadata but no selected version.
func TestGetPreservesCurrentVersionForUnknownRelease(t *testing.T) {
	snapshot := &testSnapshot{
		current:  model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1},
		releases: map[string]model.Release{"1.0.0": {SchemaVersion: "1.0.0"}},
	}
	_, err := New(testReader{snapshot: snapshot}).Get(context.Background(), "2.0.0")
	require.ErrorIs(t, err, postgres.ErrNotFound)
	current, selected, ok := RequestMetadata(err)
	require.True(t, ok)
	assert.Equal(t, "1.0.0", current.SchemaVersion)
	assert.Empty(t, selected)
}

// TestServiceCurrentReleaseFailuresPreserveMetadata verifies all release operations retain the current pointer, original cause, and unchanged error text.
func TestServiceCurrentReleaseFailuresPreserveMetadata(t *testing.T) {
	cause := errors.New("current release unavailable")
	current := model.CurrentState{SchemaVersion: "1.0.0", ActivationSequence: 1, SnapshotSequence: 1}
	service := New(testReader{snapshot: &testSnapshot{current: current, currentErr: cause}})
	for name, operation := range map[string]func() error{
		"discovery": func() error { _, err := service.Discovery(t.Context()); return err },
		"list":      func() error { _, err := service.List(t.Context(), nil, nil, 1); return err },
		"get":       func() error { _, err := service.Get(t.Context(), "1.0.0"); return err },
		"compare":   func() error { _, _, err := service.Compare(t.Context(), "1.0.0", "1.1.0"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := operation()
			require.ErrorIs(t, err, cause)
			got, selected, ok := RequestMetadata(err)
			require.True(t, ok)
			assert.Equal(t, current, got)
			assert.Empty(t, selected)
			assert.Equal(t, cause.Error(), err.Error())
		})
	}
}

// TestListPropagatesPageFailure verifies a page-query failure exposes no items and preserves resolved current metadata and cause.
func TestListPropagatesPageFailure(t *testing.T) {
	cause := errors.New("page query failed")
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0"}, releases: map[string]model.Release{"1.0.0": {SchemaVersion: "1.0.0"}}, historyErr: cause}
	page, err := New(testReader{snapshot: snapshot}).List(t.Context(), nil, nil, 10)
	require.ErrorIs(t, err, cause)
	assert.Empty(t, page.Items)
	current, _, ok := RequestMetadata(err)
	require.True(t, ok)
	assert.Equal(t, snapshot.current, current)
}

// TestCompareRejectsMissingSourceMalformedManifestsAndArtifactFailures verifies endpoint absence, invalid manifests, and artifact failures retain distinct causes and current metadata.
func TestCompareRejectsMissingSourceMalformedManifestsAndArtifactFailures(t *testing.T) {
	first := comparisonManifest(t, "1.0.0", "rev-device-v1", nil)
	second := comparisonManifest(t, "1.0.1", "rev-device-v2", first)
	cause := errors.New("artifact lookup failed")
	for _, test := range []struct {
		name   string
		mutate func(*testSnapshot)
		want   error
	}{
		{name: "missing source", mutate: func(s *testSnapshot) { delete(s.releases, "1.0.0") }, want: postgres.ErrNotFound},
		{name: "malformed source", mutate: func(s *testSnapshot) {
			value := s.releases["1.0.0"]
			value.ManifestBytes = []byte("{")
			s.releases["1.0.0"] = value
		}, want: postgres.ErrInconsistentState},
		{name: "malformed target", mutate: func(s *testSnapshot) {
			value := s.releases["1.0.1"]
			value.ManifestBytes = []byte("{")
			s.releases["1.0.1"] = value
		}, want: postgres.ErrInconsistentState},
		{name: "artifact failure", mutate: func(s *testSnapshot) { s.artifactErr = cause }, want: cause},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "1.0.1"}, releases: map[string]model.Release{
				"1.0.0": {SchemaVersion: "1.0.0", ManifestBytes: first, ActivationSequence: 1},
				"1.0.1": {SchemaVersion: "1.0.1", ManifestBytes: second, ActivationSequence: 2},
			}}
			test.mutate(snapshot)
			_, current, err := New(testReader{snapshot: snapshot}).Compare(t.Context(), "1.0.0", "1.0.1")
			require.ErrorIs(t, err, test.want)
			metadata, selected, ok := RequestMetadata(err)
			require.True(t, ok)
			assert.Equal(t, current, metadata)
			assert.Empty(t, selected)
		})
	}
}

// comparisonManifest creates canonical endpoint bytes with synchronized artifact and URI selections and an optional predecessor digest.
func comparisonManifest(t *testing.T, version, revision string, previous []byte) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "manifest", "valid-initial.json"))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(value, &document))
	document["schemaVersion"] = version
	if previous != nil {
		document["previousRelease"] = map[string]any{"schemaVersion": "1.0.0", "manifestDigest": manifestDigest(previous)}
	}
	artifactEntry := document["artifacts"].([]any)[0].(map[string]any)
	artifactEntry["artifactRevision"] = revision
	artifactEntry["canonicalUri"] = "https://registry.example.test/v1/resources/schema/device/" + revision
	document["uriCatalog"].([]any)[0].(map[string]any)["artifactRevision"] = revision
	raw, err := json.Marshal(document)
	require.NoError(t, err)
	canonical, err := jcs.Transform(raw)
	require.NoError(t, err)
	return canonical
}

// manifestDigest supplies exact-byte integrity metadata for comparison fixture manifests.
func manifestDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
