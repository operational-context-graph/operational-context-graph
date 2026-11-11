// Package schemaartifacts defines the version 1 artifact validation limits.
package schemaartifacts

const (
	// MaxIdentifierBytes bounds logical types, revisions, resource names, and media types in UTF-8 bytes.
	MaxIdentifierBytes = 255
	// MaxURIBytes bounds Uniform Resource Identifiers (URIs) and Internationalized Resource Identifiers (IRIs) in UTF-8 bytes.
	MaxURIBytes = 8192
	// MaxArtifactBytes bounds each exact manifest, primary payload, or additional resource.
	MaxArtifactBytes = 16 * 1024 * 1024
	// MaxJSONDepth bounds JSON value traversal, starting at zero for the root.
	MaxJSONDepth = 64
	// MaxObjectMembers bounds the number of members in each artifact JSON object.
	MaxObjectMembers = 256
	// MaxArrayEntries bounds each artifact JSON array or format-defined collection.
	MaxArrayEntries = 10000
	// MaxJSONStringBytes bounds each decoded JSON string or member name in UTF-8 bytes.
	MaxJSONStringBytes = 1024 * 1024
)
