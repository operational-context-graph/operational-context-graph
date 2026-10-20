// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

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
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

type testReader struct {
	snapshot *testSnapshot
	err      error
}

func (r testReader) Read(ctx context.Context, fn func(context.Context, Snapshot) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(ctx, r.snapshot)
}

type testSnapshot struct {
	current    model.CurrentState
	releases   map[string]model.Release
	calls      []int64
	limits     []int
	history    []model.Release
	ancestor   *bool
	lineageErr error
	artifacts  map[string]model.Artifact
}

func (s *testSnapshot) CurrentState() model.CurrentState { return s.current }
func (s *testSnapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	return s.Release(ctx, s.current.SchemaVersion)
}
func (s *testSnapshot) Release(_ context.Context, version string) (model.Release, error) {
	value, ok := s.releases[version]
	if !ok {
		return model.Release{}, postgres.ErrNotFound
	}
	return value, nil
}
func (s *testSnapshot) ReleasesAt(_ context.Context, before *int64, maximum int64, limit int) ([]model.Release, error) {
	s.calls = append(s.calls, maximum)
	s.limits = append(s.limits, limit)
	return s.history, nil
}
func (s *testSnapshot) IsAncestor(context.Context, string, string) (bool, error) {
	if s.lineageErr != nil {
		return false, s.lineageErr
	}
	if s.ancestor == nil {
		return true, nil
	}
	return *s.ancestor, nil
}
func (s *testSnapshot) Artifact(_ context.Context, revision string) (model.Artifact, error) {
	if value, ok := s.artifacts[revision]; ok {
		return value, nil
	}
	return model.Artifact{}, postgres.ErrNotFound
}
func (*testSnapshot) ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error) {
	return model.Artifact{}, postgres.ErrNotFound
}
func (*testSnapshot) ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) Resources(context.Context, string) ([]model.Resource, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) Resource(context.Context, string, string) (model.Resource, error) {
	return model.Resource{}, postgres.ErrNotFound
}
func (*testSnapshot) Dependencies(context.Context, string) ([]model.Dependency, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) ContextBindings(context.Context, string, string) ([]model.ContextBinding, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) StorageEntries(context.Context, string, string) ([]model.StorageEntry, error) {
	return nil, postgres.ErrNotFound
}
func (*testSnapshot) URIEntry(context.Context, string, string) (model.URIEntry, error) {
	return model.URIEntry{}, postgres.ErrNotFound
}
func (*testSnapshot) SourceMapping(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}
func (*testSnapshot) SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error) {
	return model.SourceMappingDetail{}, postgres.ErrNotFound
}

func TestListBindsCallerCursorToOriginalSnapshot(t *testing.T) {
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "2.0.0", ActivationSequence: 2, SnapshotSequence: 9}, releases: map[string]model.Release{"2.0.0": {SchemaVersion: "2.0.0"}}}
	requested := int64(5)
	page, err := New(testReader{snapshot: snapshot}).List(context.Background(), nil, &requested, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 5, page.Current.SnapshotSequence)
	require.Equal(t, []int64{5}, snapshot.calls)
}

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

func TestComparePropagatesMissingReleaseAndStorageFailure(t *testing.T) {
	snapshot := &testSnapshot{current: model.CurrentState{SchemaVersion: "1.0.0"}, releases: map[string]model.Release{"1.0.0": {SchemaVersion: "1.0.0"}}}
	_, _, err := New(testReader{snapshot: snapshot}).Compare(context.Background(), "1.0.0", "2.0.0")
	require.ErrorIs(t, err, postgres.ErrNotFound)
	_, _, err = New(testReader{err: errors.New("database unavailable")}).Compare(context.Background(), "1.0.0", "2.0.0")
	require.Error(t, err)
}

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
	assert.Equal(t, "update", changes.Artifacts[0].Operation)
	assert.Equal(t, "rev-device-v1", changes.Artifacts[0].BeforeRevision)
	assert.Equal(t, "rev-device-v2", changes.Artifacts[0].AfterRevision)
	assert.Equal(t, "breaking", changes.Artifacts[0].Impact)
	assert.Equal(t, "breaking", changes.Compatibility)
}

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

func comparisonManifest(t *testing.T, version, revision string, previous []byte) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "..", "schema-artifacts", "manifest", "testdata", "valid-initial.json"))
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

func manifestDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha-256:" + hex.EncodeToString(sum[:])
}
