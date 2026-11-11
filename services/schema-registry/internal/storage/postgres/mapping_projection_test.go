package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

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
	rows, err := MappingDescriptorProjection(value, item, descriptor)
	require.NoError(t, err)
	digest := item.Resources[0].Digest
	require.Equal(t, []string{
		projectionRow("mapping", "mapping-r1", "incoming", "source-r1", "1.0.0", "opaque", "1.0.0", "publisher", "shared.bin", digest),
		projectionRow("transformation", "mapping-r1", "0", "device", "target-r1", "one", "shared.bin", digest, "", ""),
	}, rows)
	descriptor.Programs.Condition = nil
	rows, err = MappingDescriptorProjection(value, item, descriptor)
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
			rows, err := MappingDescriptorProjection(value, item, descriptor)
			require.ErrorIs(t, err, ErrInconsistentState)
			require.Nil(t, rows, "invalid projections must not expose partial rows")
		})
	}
}
