// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package schemaartifacts defines the version 1 artifact validation limits.
package schemaartifacts

const (
	// MaxIdentifierBytes bounds logical types, revisions, resource names, and media types in UTF-8 bytes.
	MaxIdentifierBytes = 255
	// MaxURIBytes bounds artifact Uniform Resource Identifiers (URIs) in UTF-8 bytes.
	MaxURIBytes = 8192
	// MaxArtifactBytes bounds each exact manifest, primary payload, or additional resource.
	MaxArtifactBytes = 16 * 1024 * 1024
)
