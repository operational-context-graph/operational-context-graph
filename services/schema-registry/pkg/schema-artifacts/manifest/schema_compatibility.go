package manifest

import (
	"reflect"
	"slices"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// schemaImpact classifies schema updates using exact bytes and unchanged artifact relations.
// Supported annotation edits are operational; proven optional additions are additive.
// Changed documents with references remain breaking without a reference-aware equivalence proof.
// Missing or inconclusive evidence is breaking.
func schemaImpact(before, after artifactmodel.Artifact, context Context) Impact {
	left, leftOK := context.Artifacts[artifactmodel.Dependency{Kind: before.Kind, Type: before.Type, Revision: before.Revision}]
	right, rightOK := context.Artifacts[artifactmodel.Dependency{Kind: after.Kind, Type: after.Type, Revision: after.Revision}]
	if !leftOK || !rightOK || !sameArtifactRelations(before, after) || left.Payload.MediaType != artifactmodel.SchemaMediaType || right.Payload.MediaType != artifactmodel.SchemaMediaType {
		return ImpactBreaking
	}
	leftValue, err := strictjson.Parse(left.PayloadBytes)
	if err != nil {
		return ImpactBreaking
	}
	rightValue, err := strictjson.Parse(right.PayloadBytes)
	if err != nil {
		return ImpactBreaking
	}
	unchanged := reflect.DeepEqual(leftValue, rightValue)
	// These parsed values are private copies; caller-owned artifacts and bytes remain unchanged.
	for _, value := range []any{leftValue, rightValue} {
		if err := walkSchema(value, before.CanonicalURI, func(node map[string]any, _ string) error {
			if dialect, ok := node["$schema"].(string); ok && dialect != "https://json-schema.org/draft/2020-12/schema" {
				return invalid("schema compatibility uses an unsupported dialect")
			}
			// References can use annotation values as schemas, including through JSON Pointers.
			// Without a reference-aware equivalence proof, annotation removal is unsafe.
			for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
				if _, exists := node[key]; exists && !unchanged {
					return invalid("schema compatibility has unproven reference effects")
				}
			}
			for _, key := range []string{"title", "description", "$comment", "examples"} {
				delete(node, key)
			}
			return nil
		}); err != nil {
			return ImpactBreaking
		}
	}
	if reflect.DeepEqual(leftValue, rightValue) {
		return ImpactOperational
	}
	if optionalPropertyAddition(leftValue, rightValue) {
		return ImpactAdditive
	}
	return ImpactBreaking
}

// optionalPropertyAddition reports whether right adds only optional properties to closed schemas
// in left.
// Inputs MUST be parsed schema values with supported annotation keywords already removed.
// Existing constraints remain identical except for supported nested additions.
// It returns false when references or unknown keywords prevent proving backward compatibility.
func optionalPropertyAddition(left, right any) bool {
	before, ok := left.(map[string]any)
	if !ok {
		return false
	}
	after, ok := right.(map[string]any)
	if !ok || before["type"] != "object" || before["additionalProperties"] != false {
		return false
	}
	for key, value := range before {
		switch key {
		case "type", "required", "additionalProperties", "$schema":
			if !reflect.DeepEqual(value, after[key]) {
				return false
			}
		case "properties":
		default:
			return false
		}
	}
	for key := range after {
		if key != "properties" {
			if _, ok := before[key]; !ok {
				return false
			}
		}
	}
	previous, _ := before["properties"].(map[string]any)
	current, _ := after["properties"].(map[string]any)
	changed := false
	required, _ := after["required"].([]any)
	for name, schema := range current {
		old, exists := previous[name]
		if !exists {
			if slices.Contains(required, any(name)) {
				return false
			}
			changed = true
		} else if !reflect.DeepEqual(old, schema) {
			if !optionalPropertyAddition(old, schema) {
				return false
			}
			changed = true
		}
	}
	for name, schema := range previous {
		if _, ok := current[name]; !ok {
			return false
		}
		// Existing references can reach the changed enclosing schema, including through negation.
		if err := walkSchema(schema, "https://comparison.invalid/schema", func(node map[string]any, _ string) error {
			for _, key := range []string{"$id", "$ref", "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor"} {
				if _, ok := node[key]; ok {
					return invalid("schema addition has unproven reference effects")
				}
			}
			return nil
		}); err != nil {
			return false
		}
	}
	return changed
}
