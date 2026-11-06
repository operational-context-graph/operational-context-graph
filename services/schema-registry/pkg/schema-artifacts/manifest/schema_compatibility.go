// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"reflect"

	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// annotationOnly proves both schema payloads differ only in allowed annotations and preserve comparison-relevant artifact relations.
func annotationOnly(before, after artifactmodel.Artifact, context Context) bool {
	left, ok := context.Artifacts[artifactmodel.Dependency{Kind: before.Kind, Type: before.Type, Revision: before.Revision}]
	if !ok {
		return false
	}
	right, ok := context.Artifacts[artifactmodel.Dependency{Kind: after.Kind, Type: after.Type, Revision: after.Revision}]
	if !ok {
		return false
	}
	if !sameSchemaRelations(before, after) || left.Payload.MediaType != artifactmodel.SchemaMediaType || right.Payload.MediaType != artifactmodel.SchemaMediaType {
		return false
	}
	leftValue, err := strictjson.Parse(left.PayloadBytes)
	if err != nil {
		return false
	}
	rightValue, err := strictjson.Parse(right.PayloadBytes)
	if err != nil {
		return false
	}
	return equalWithoutAnnotations(leftValue, rightValue)
}

// sameSchemaRelations compares schema metadata after excluding revisions, creation releases, payloads, canonical identifiers, and additional resources.
func sameSchemaRelations(left, right artifactmodel.Artifact) bool {
	left.Revision = ""
	right.Revision = ""
	left.CreatedUnderSchemaVersion = ""
	right.CreatedUnderSchemaVersion = ""
	left.Payload = artifactmodel.Descriptor{}
	right.Payload = artifactmodel.Descriptor{}
	left.PayloadBytes = nil
	right.PayloadBytes = nil
	left.CanonicalURI = ""
	right.CanonicalURI = ""
	left.Resources = nil
	right.Resources = nil
	return reflect.DeepEqual(left, right)
}

// equalWithoutAnnotations recursively compares JSON values while ignoring title, description, $comment, and examples object members.
func equalWithoutAnnotations(left, right any) bool {
	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if leftOK != rightOK {
		return false
	}
	if leftOK {
		for key, leftValue := range leftMap {
			if key == "title" || key == "description" || key == "$comment" || key == "examples" {
				continue
			}
			rightValue, ok := rightMap[key]
			if !ok || !equalWithoutAnnotations(leftValue, rightValue) {
				return false
			}
		}
		for key := range rightMap {
			if key == "title" || key == "description" || key == "$comment" || key == "examples" {
				continue
			}
			if _, ok := leftMap[key]; !ok {
				return false
			}
		}
		return true
	}
	leftArray, leftOK := left.([]any)
	rightArray, rightOK := right.([]any)
	if leftOK != rightOK {
		return false
	}
	if leftOK {
		if len(leftArray) != len(rightArray) {
			return false
		}
		for index := range leftArray {
			if !equalWithoutAnnotations(leftArray[index], rightArray[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}
