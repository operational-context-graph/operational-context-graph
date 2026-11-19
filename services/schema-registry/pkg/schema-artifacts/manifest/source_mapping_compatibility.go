package manifest

import (
	"reflect"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	sourcemapping "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/source-mapping"
)

// equivalentSourceMapping reports whether source mapping revisions have identical parsed descriptors and
// revision-independent metadata. Validated resource descriptors establish program-byte identity.
//
// This function never interprets programs or claims behavioral equivalence for changed programs.
// Missing bytes, invalid descriptors, or changed behavior-bearing fields return false.
func equivalentSourceMapping(before, after artifact.Artifact, context Context) bool {
	if !sameArtifactRelations(before, after) {
		return false
	}
	left, ok := context.Artifacts[artifact.Dependency{Kind: before.Kind, Type: before.Type, Revision: before.Revision}]
	if !ok || left.Payload.MediaType != sourcemapping.DescriptorMediaType {
		return false
	}
	right, ok := context.Artifacts[artifact.Dependency{Kind: after.Kind, Type: after.Type, Revision: after.Revision}]
	if !ok || right.Payload.MediaType != sourcemapping.DescriptorMediaType {
		return false
	}
	a, err := sourcemapping.Parse(left.PayloadBytes)
	if err != nil {
		return false
	}
	b, err := sourcemapping.Parse(right.PayloadBytes)
	return err == nil && reflect.DeepEqual(a, b)
}
