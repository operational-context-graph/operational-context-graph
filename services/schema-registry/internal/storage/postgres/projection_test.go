package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

/*
Mapping projection contract scope

Each case creates fresh source, target, mapping, and descriptor values.
The fixture intentionally shares one opaque resource between its condition and transformation.
Valid projections preserve exact rows and represent condition absence through empty binding fields.
Each invalid case changes one identity, dependency, target, or program binding.
Any inconsistency rejects the complete projection without exposing partial rows.

Program resources remain opaque throughout these tests.
No test interprets or executes condition or transformation bytes.
The tests use only caller-owned inputs and require no database or container.
*/

// projectionMappingFixture builds fresh inputs with one condition and transformation sharing an opaque program resource.
// Each caller owns its slices and pointers, so mutation cases remain independent.
func projectionMappingFixture() (manifest.Manifest, artifact.Artifact, sourcemapping.Descriptor) {
	program := sourcemapping.ProgramReference{ProgramRef: "shared.bin", ProgramDigest: manifestDigest([]byte("opaque"))}
	source := artifact.Artifact{Kind: artifact.KindSchema, Type: "incoming", Revision: "source-r1", SchemaClass: artifact.SchemaClassSource}
	target := artifact.Artifact{Kind: artifact.KindSchema, Type: "device", Revision: "target-r1", SchemaClass: artifact.SchemaClassEntity}
	item := artifact.Artifact{
		Kind: artifact.KindSourceMapping, Type: "device-mapping", Revision: "mapping-r1", TargetSchema: "1.0.0",
		Dependencies: []artifact.Dependency{{Kind: source.Kind, Type: source.Type, Revision: source.Revision}, {Kind: target.Kind, Type: target.Type, Revision: target.Revision}},
		Resources:    []artifact.Resource{{Name: program.ProgramRef, Role: "program", Descriptor: artifact.Descriptor{Digest: program.ProgramDigest}}},
	}
	descriptor := sourcemapping.Descriptor{
		ID: item.Type, SourceSchema: sourcemapping.SchemaReference{ID: source.Type, Revision: source.Revision},
		Engine:   sourcemapping.Engine{Language: "opaque", LanguageVersion: "1.0.0", ExecutionProfile: "publisher"},
		Programs: sourcemapping.Programs{Condition: &program, Transformations: []sourcemapping.Transformation{{ProgramReference: program, TargetSchema: sourcemapping.SchemaReference{ID: target.Type, Version: item.TargetSchema}, Cardinality: sourcemapping.CardinalityOne}}},
	}
	return manifest.Manifest{SchemaVersion: "1.0.0", Artifacts: []artifact.Artifact{source, target, item}}, item, descriptor
}

// TestMappingProjectionPreservesSharedBindings verifies exact projection rows for shared condition and transformation resources.
// Removing the optional condition clears only its projected binding fields.
func TestMappingProjectionPreservesSharedBindings(t *testing.T) {
	t.Parallel()
	value, item, descriptor := projectionMappingFixture()
	rows, err := mappingDescriptorProjection(value, item, descriptor)
	require.NoError(t, err)
	digest := item.Resources[0].Digest
	require.Equal(t, []string{
		projectionRow("mapping", "mapping-r1", "incoming", "source-r1", "1.0.0", "opaque", "1.0.0", "publisher", "shared.bin", digest),
		projectionRow("transformation", "mapping-r1", "0", "device", "target-r1", "one", "shared.bin", digest, "", ""),
	}, rows)
	descriptor.Programs.Condition = nil
	rows, err = mappingDescriptorProjection(value, item, descriptor)
	require.NoError(t, err)
	require.Equal(t, projectionRow("mapping", "mapping-r1", "incoming", "source-r1", "1.0.0", "opaque", "1.0.0", "publisher", "", ""), rows[0])
}

// TestMappingProjectionRejectsInconsistentBindings changes one identity, dependency, target, or program binding per case.
// Every inconsistency returns the stable error without exposing partial projection rows.
func TestMappingProjectionRejectsInconsistentBindings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*manifest.Manifest, *artifact.Artifact, *sourcemapping.Descriptor)
	}{
		{name: "mapping identity", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.ID = "other"
		}},
		{name: "missing source dependency", mutate: func(_ *manifest.Manifest, item *artifact.Artifact, _ *sourcemapping.Descriptor) {
			item.Dependencies = item.Dependencies[1:]
		}},
		{name: "source revision mismatch", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.SourceSchema.Revision = "other-r1"
		}},
		{name: "source not selected", mutate: func(value *manifest.Manifest, _ *artifact.Artifact, _ *sourcemapping.Descriptor) {
			value.Artifacts = value.Artifacts[1:]
		}},
		{name: "target not canonical", mutate: func(value *manifest.Manifest, _ *artifact.Artifact, _ *sourcemapping.Descriptor) {
			value.Artifacts[1].SchemaClass = artifact.SchemaClassSource
		}},
		{name: "target version mismatch", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.Programs.Transformations[0].TargetSchema.Version = "2.0.0"
		}},
		{name: "missing target dependency", mutate: func(_ *manifest.Manifest, item *artifact.Artifact, _ *sourcemapping.Descriptor) {
			item.Dependencies = item.Dependencies[:1]
		}},
		{name: "condition name mismatch", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.Programs.Condition.ProgramRef = "missing.bin"
		}},
		{name: "condition digest mismatch", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.Programs.Condition.ProgramDigest = manifestDigest(nil)
		}},
		{name: "resource role mismatch", mutate: func(_ *manifest.Manifest, item *artifact.Artifact, _ *sourcemapping.Descriptor) {
			item.Resources[0].Role = "metadata"
		}},
		{name: "transformation program mismatch", mutate: func(_ *manifest.Manifest, _ *artifact.Artifact, descriptor *sourcemapping.Descriptor) {
			descriptor.Programs.Transformations[0].ProgramRef = "missing.bin"
		}},
		{name: "unreferenced program", mutate: func(_ *manifest.Manifest, item *artifact.Artifact, _ *sourcemapping.Descriptor) {
			extra := item.Resources[0]
			extra.Name = "unused.bin"
			item.Resources = append(item.Resources, extra)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, item, descriptor := projectionMappingFixture()
			test.mutate(&value, &item, &descriptor)
			rows, err := mappingDescriptorProjection(value, item, descriptor)
			require.ErrorIs(t, err, ErrInconsistentState)
			require.Nil(t, rows, "invalid projections must not expose partial rows")
		})
	}
}

// TestDirectProjectionRows verifies empty, single-row, missing, extra, and altered projections without separate evidence queries.
func TestDirectProjectionRows(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		family model.ProjectionFamily
		mutate func(*queryTestRows)
		want   error
	}{
		{name: "single release row", family: model.ProjectionRelease},
		{name: "empty bindings", family: model.ProjectionBinding},
		{name: "empty resources", family: model.ProjectionResource},
		{name: "empty dependencies", family: model.ProjectionDependency},
		{name: "empty mappings", family: model.ProjectionSourceMapping},
		{name: "missing release row", family: model.ProjectionRelease, mutate: func(rows *queryTestRows) {
			rows.values = nil
		}, want: ErrInconsistentState},
		{name: "extra release row", family: model.ProjectionRelease, mutate: func(rows *queryTestRows) {
			rows.values = append(rows.values, rows.values[0])
		}, want: ErrInconsistentState},
		{name: "altered release row", family: model.ProjectionRelease, mutate: func(rows *queryTestRows) {
			rows.values[0][0] = projectionRow("altered")
		}, want: ErrInconsistentState},
		{name: "unexpected binding", family: model.ProjectionBinding, mutate: func(rows *queryTestRows) {
			rows.values = [][]any{{projectionRow("unexpected-schema", "unexpected-context")}}
		}, want: ErrInconsistentState},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := new(queryTestTx)
			tx.Test(t)
			rows := expectVerifiedProjection(t, tx, test.family)
			if test.mutate != nil {
				test.mutate(rows)
			}
			err := (&Snapshot{tx: tx}).VerifyProjection(t.Context(), "1.0.0", test.family)
			require.ErrorIs(t, err, test.want)
			tx.AssertExpectations(t)
			require.True(t, rows.closed)
		})
	}
}

// TestDirectProjectionRejectsWriterFingerprint verifies producer metadata cannot authorize rows that disagree with the manifest.
func TestDirectProjectionRejectsWriterFingerprint(t *testing.T) {
	t.Parallel()
	wrongRow := projectionRow("unexpected-schema", "unexpected-context")
	fingerprint := sha256.Sum256([]byte(wrongRow))
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "fixtures", "manifest", "valid-initial.json"))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	// Namespaced extensions are producer-controlled data, not verification authority.
	document["extensions"] = map[string]any{"org.example.writer-fingerprint": hex.EncodeToString(fingerprint[:])}
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	raw, err = jcs.Transform(raw)
	require.NoError(t, err)
	_, err = manifest.Parse(raw)
	require.NoError(t, err)
	tx := new(queryTestTx)
	tx.Test(t)
	query, err := projectionQuery(model.ProjectionBinding)
	require.NoError(t, err)
	tx.On("QueryRow", mock.Anything, sqlContains("SELECT EXISTS"), []any{"1.0.0"}).Return(valueRow(true)).Once()
	tx.On("Query", mock.Anything, query, []any{"1.0.0"}).Return(&queryTestRows{values: [][]any{{wrongRow}}}, nil).Once()
	tx.On("QueryRow", mock.Anything, sqlContains("SELECT manifest_bytes"), []any{"1.0.0"}).Return(valueRow(raw)).Once()
	err = (&Snapshot{tx: tx}).VerifyProjection(t.Context(), "1.0.0", model.ProjectionBinding)
	require.ErrorIs(t, err, ErrInconsistentState)
	tx.AssertExpectations(t)
}

// TestDirectProjectionReadFailures preserves deadline causes from both projection-row and authoritative-manifest reads.
func TestDirectProjectionReadFailures(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"projection rows", "manifest bytes"} {
		t.Run(stage, func(t *testing.T) {
			tx := new(queryTestTx)
			tx.Test(t)
			query, err := projectionQuery(model.ProjectionBinding)
			require.NoError(t, err)
			tx.On("QueryRow", mock.Anything, sqlContains("SELECT EXISTS"), []any{"1.0.0"}).Return(valueRow(true)).Once()
			if stage == "projection rows" {
				tx.On("Query", mock.Anything, query, []any{"1.0.0"}).Return(nil, context.DeadlineExceeded).Once()
			} else {
				tx.On("Query", mock.Anything, query, []any{"1.0.0"}).Return(&queryTestRows{}, nil).Once()
				tx.On("QueryRow", mock.Anything, sqlContains("SELECT manifest_bytes"), []any{"1.0.0"}).Return(failingRow(context.DeadlineExceeded)).Once()
			}
			err = (&Snapshot{tx: tx}).VerifyProjection(t.Context(), "1.0.0", model.ProjectionBinding)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			tx.AssertExpectations(t)
		})
	}
}

// TestMappingDescriptorByteIntegrity verifies digest and size mismatches fail before parsing or returning projection rows.
func TestMappingDescriptorByteIntegrity(t *testing.T) {
	t.Parallel()
	for _, mismatch := range []string{"none", "digest", "size"} {
		t.Run(mismatch, func(t *testing.T) {
			value, item, _ := projectionMappingFixture()
			payload, err := json.Marshal(map[string]any{
				"$schema": "https://example.test/source-mapping", "formatVersion": "0.1.0", "id": item.Type,
				"sourceSchema": map[string]any{"id": "incoming", "revision": "source-r1"},
				"engine":       map[string]any{"language": "opaque", "languageVersion": "1.0.0", "executionProfile": "publisher"},
				"programs": map[string]any{"transformations": []any{map[string]any{
					"programRef": "shared.bin", "programDigest": item.Resources[0].Digest, "cardinality": "one",
					"targetSchema": map[string]any{"id": "device", "version": "1.0.0"},
				}}},
			})
			require.NoError(t, err)
			item.Payload.Digest = manifestDigest(payload)
			item.Payload.Size = int64(len(payload))
			switch mismatch {
			case "digest":
				item.Payload.Digest = manifestDigest(nil)
			case "size":
				item.Payload.Size--
			}
			value.Artifacts[2] = item
			tx := new(queryTestTx)
			tx.Test(t)
			tx.On("QueryRow", mock.Anything, sqlContains("SELECT payload_bytes"), []any{"mapping-r1"}).Return(valueRow(payload)).Once()
			rows, err := sourceMappingProjectionRows(t.Context(), tx, value)
			if mismatch == "none" {
				require.NoError(t, err)
				require.Len(t, rows, 2)
			} else {
				require.ErrorIs(t, err, ErrInconsistentState)
				require.Nil(t, rows)
			}
			tx.AssertExpectations(t)
		})
	}
}

// TestMappingDescriptorReadFailures distinguishes missing required bytes from operational failures without returning partial rows.
func TestMappingDescriptorReadFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cause error
		want  error
	}{
		{name: "missing descriptor", cause: pgx.ErrNoRows, want: ErrInconsistentState},
		{name: "descriptor deadline", cause: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "descriptor cancellation", cause: context.Canceled, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, _, _ := projectionMappingFixture()
			tx := new(queryTestTx)
			tx.Test(t)
			tx.On("QueryRow", mock.Anything, sqlContains("SELECT payload_bytes"), []any{"mapping-r1"}).Return(failingRow(test.cause)).Once()
			rows, err := sourceMappingProjectionRows(t.Context(), tx, value)
			require.ErrorIs(t, err, test.want)
			require.Nil(t, rows)
			tx.AssertExpectations(t)
		})
	}
}
