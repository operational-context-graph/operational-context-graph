// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package model

// Descriptor combines immutable artifact metadata, resources, and dependencies.
type Descriptor struct {
	Artifact           Artifact
	Resources          []Resource
	Dependencies       []Dependency
	SourceArtifactType string
}

// Mapping is an enriched, release-selected source mapping.
type Mapping struct {
	Descriptor Descriptor
	Detail     SourceMappingDetail
}

// MappingPackage contains one release-consistent mapping and its dependencies.
type MappingPackage struct {
	Mapping  Descriptor
	Detail   SourceMappingDetail
	Programs []Resource
	Schemas  []Descriptor
	Stores   []StorageEntry
}
