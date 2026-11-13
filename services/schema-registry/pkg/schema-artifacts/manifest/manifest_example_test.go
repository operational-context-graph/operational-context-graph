package manifest_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

// ExampleManifest_Validate validates an initial schema release containing one JSON Schema.
func ExampleManifest_Validate() {
	const fixtures = "../../../test/fixtures"

	// Parse the canonical schema release manifest. Parsing does not load artifact bytes.
	releaseManifest, err := manifest.ParseFromFile(filepath.Join(fixtures, "manifest/examples/initial.json"))
	if err != nil {
		panic(err)
	}

	// Load the selected schema's exact bytes from its bundle path.
	// Note that the parsed schema release manifest remains metadata-only because the 'schema' variable
	// is a struct copy, i.e., the payload bytes for the schema are not added to the release manifest.
	schema := releaseManifest.Artifacts[0]
	schema.PayloadBytes, err = os.ReadFile(filepath.Join(fixtures, schema.Payload.BundlePath))
	if err != nil {
		panic(err)
	}

	// Bind the artifact bytes by immutable identity. An initial release has no history.
	context := manifest.Context{
		RegistryOrigin: "https://registry.example.test",
		Artifacts: map[artifact.Dependency]artifact.Artifact{
			{Kind: schema.Kind, Type: schema.Type, Revision: schema.Revision}: schema,
		},
	}

	// Validate byte integrity, schema format, catalogs, and initial-release rules.
	if err := releaseManifest.Validate(context); err != nil {
		panic(err)
	}
	fmt.Println("Validated schema release:", releaseManifest.SchemaVersion)

	// Output: Validated schema release: 1.0.0
}
