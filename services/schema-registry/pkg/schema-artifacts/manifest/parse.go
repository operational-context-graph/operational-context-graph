package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/gowebpki/jcs"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// extensionPattern matches lowercase, dot-separated namespace labels for publisher extension keys.
var extensionPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// ParseFromFile accepts a release manifest file that satisfies every representation and metadata requirement of [Parse].
//
// Callers MUST restrict untrusted paths when filesystem access requires a boundary.
//
// The manifest byte limit does not bound the initial file read.
// Read errors wrap the underlying filesystem error; content errors follow [Parse]'s error behavior.
func ParseFromFile(path string) (Manifest, error) {
	// #nosec G304 -- this file-loading API intentionally reads the caller-selected path.
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	return Parse(data)
}

// Parse checks the representation and metadata of a schema release manifest.
//
// For the JSON representation, it checks that:
//   - The document is a UTF-8 Internet JSON (I-JSON) object without duplicate member names.
//   - Submitted bytes equal their JSON Canonicalization Scheme (JCS) serialization, without a byte order mark or trailing newline.
//   - Document size, nesting, object members, array entries, and strings stay within the Registry's parsing limits.
//   - Numbers fit finite binary64, and integral values lie between -9007199254740991 and 9007199254740991.
//
// For format declarations and provenance, it checks that:
//   - Format-defined objects contain only known fields, with required fields and non-empty strings present.
//   - The format version is 0.1.0.
//   - Published schema versions use MAJOR.MINOR.PATCH without leading zeroes or suffixes.
//   - Repository and document identifiers are absolute, within the URI byte limit, and without non-empty queries or fragments.
//   - Git commits contain 40 or 64 lowercase hexadecimal digits.
//
// For artifact selections and byte descriptors, it checks that:
//   - The artifact array is non-empty, and selections have valid creation versions and kind-specific fields.
//   - Logical types match [a-z0-9]+(?:-[a-z0-9]+)* within the identifier byte limit.
//   - Revisions use bounded RFC 3986 unreserved characters, excluding the values "." and "..".
//   - Primary payload digests use sha-256: followed by 64 lowercase hexadecimal digits, and sizes are non-negative integers.
//   - Primary bundle paths use slash separators, without leading slashes, backslashes, or empty, dot, or parent segments.
//   - Payload and resource bundle paths are unique across the release.
//
// For bindings, catalogs, changes, and extensions, it checks that:
//   - Bindings select at least one context.
//   - Storage entries select schemas and non-empty lists of registered stores.
//   - Change entries use supported operations and impacts with operation-appropriate revision selectors.
//   - Extension names use lowercase, dot-separated reverse-domain syntax.
//   - Format-defined arrays have strictly increasing UTF-16 sort keys, excluding duplicate keys.
//
// These checks do not establish descriptor integrity, complete resource metadata validity, or reference resolution.
// They do not establish release lineage, historical immutability, or the accuracy of declared changes.
// Extension namespace ownership and extension semantics are not checked.
//
// The data contains manifest bytes only, not a bundle or embedded artifact payloads.
// On success, Raw owns a copy of data and selected artifacts contain metadata without payload or resource bytes.
// Use [Validate] to also check caller-supplied bytes and release history.
func Parse(data []byte) (Manifest, error) {
	if len(data) > schemaartifacts.MaxArtifactBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", errInvalid)
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || bytes.HasSuffix(data, []byte("\n")) {
		return Manifest{}, fmt.Errorf("%w: manifest must not contain a byte order mark or trailing newline", errInvalid)
	}
	// Reject duplicate members and profile violations before canonicalization can obscure the submitted representation.
	value, err := strictjson.Object(data)
	if err != nil {
		if errors.Is(err, strictjson.ErrUnicode) {
			return Manifest{}, fmt.Errorf("%w: parse manifest: %w", ErrInvalid, err)
		}
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	// Equality preserves the publisher's authoritative bytes instead of silently accepting a normalized manifest.
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return Manifest{}, fmt.Errorf("%w: manifest is not JCS canonical", errInvalid)
	}
	if validationErr := strictjson.RejectUnknownFields(value, "$schema", "formatVersion", "schemaVersion", "previousRelease", "source", "artifacts", "schemaContextBindings", "uriCatalog", "storageCatalog", "changes", "extensions"); validationErr != nil {
		return Manifest{}, fmt.Errorf("%w: manifest fields: %w", errInvalid, validationErr)
	}
	m := Manifest{Raw: append([]byte(nil), data...)}
	if m.Schema, err = requiredString(value, "$schema"); err != nil {
		return Manifest{}, err
	}
	if m.FormatVersion, err = requiredEnum[Version](value, "formatVersion"); err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion, err = requiredString(value, "schemaVersion"); err != nil {
		return Manifest{}, err
	}
	if _, exists := value["previousRelease"]; !exists {
		return Manifest{}, invalid("previousRelease is required")
	}
	if m.PreviousRelease, err = parsePrevious(value["previousRelease"]); err != nil {
		return Manifest{}, err
	}
	if m.Source, err = parseSource(value["source"]); err != nil {
		return Manifest{}, err
	}
	if m.Artifacts, err = parseArtifacts(value["artifacts"]); err != nil {
		return Manifest{}, err
	}
	if m.SchemaContextBindings, err = parseBindings(value["schemaContextBindings"]); err != nil {
		return Manifest{}, err
	}
	if m.URICatalog, err = parseURIs(value["uriCatalog"]); err != nil {
		return Manifest{}, err
	}
	if m.StorageCatalog, err = parseStorage(value["storageCatalog"]); err != nil {
		return Manifest{}, err
	}
	if raw, ok := value["changes"]; ok {
		if m.Changes, err = parseChanges(raw); err != nil {
			return Manifest{}, err
		}
	}
	if raw, ok := value["extensions"]; ok {
		if m.Extensions, err = parseExtensions(raw); err != nil {
			return Manifest{}, err
		}
	}
	if err := m.validateStructural(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// parsePrevious checks the predecessor declaration, accepting null for an initial release.
// For a predecessor object, it checks that:
//   - Only schemaVersion and manifestDigest are present, and neither field is missing.
//   - The version uses published MAJOR.MINOR.PATCH syntax.
//   - The digest uses sha-256: followed by 64 lowercase hexadecimal digits.
//
// The predecessor's existence and position in release history are not checked.
func parsePrevious(raw any) (*PreviousRelease, error) {
	if raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("previousRelease must be null or an object")
	}
	if err := strictjson.RejectUnknownFields(object, "schemaVersion", "manifestDigest"); err != nil {
		return nil, err
	}
	version, err := requiredString(object, "schemaVersion")
	if err != nil {
		return nil, err
	}
	digest, err := requiredString(object, "manifestDigest")
	if err != nil {
		return nil, err
	}
	if !artifactmodel.ValidVersion(version) || !artifactmodel.ValidDigest(digest) {
		return nil, invalid("invalid previous release")
	}
	return &PreviousRelease{version, digest}, nil
}

// parseSource checks that source is an object containing only non-empty repository and commit strings.
// URI and Git object identifier syntax are checked by [Manifest.validateStructural], not here.
func parseSource(raw any) (Source, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return Source{}, invalid("source must be an object")
	}
	if err := strictjson.RejectUnknownFields(object, "repository", "commit"); err != nil {
		return Source{}, err
	}
	repository, err := requiredString(object, "repository")
	if err != nil {
		return Source{}, err
	}
	commit, err := requiredString(object, "commit")
	if err != nil {
		return Source{}, err
	}
	return Source{repository, commit}, nil
}

// parseArtifacts checks artifact entry shapes. It checks that:
//   - The input is a bounded array of objects with registered kinds and all required metadata fields.
//   - Entries contain no unknown fields or empty metadata strings.
//   - Resources, dependencies, descriptors, and extensions pass their respective parsing checks.
//
// Logical identity syntax, kind-specific field constraints, ordering, and uniqueness are not checked here.
func parseArtifacts(raw any) ([]artifactmodel.Artifact, error) {
	values, err := array(raw, "artifacts")
	if err != nil {
		return nil, err
	}
	result := make([]artifactmodel.Artifact, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("artifact must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision", "createdUnderSchemaVersion", "schemaClass", "targetSchemaVersion", "canonicalUri", "payload", "resources", "dependencies", "extensions"); validationErr != nil {
			return nil, validationErr
		}
		item := artifactmodel.Artifact{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.Type, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Revision, err = requiredString(object, "artifactRevision"); err != nil {
			return nil, err
		}
		if item.CreatedUnderSchemaVersion, err = requiredString(object, "createdUnderSchemaVersion"); err != nil {
			return nil, err
		}
		if item.CanonicalURI, err = requiredString(object, "canonicalUri"); err != nil {
			return nil, err
		}
		if item.SchemaClass, err = optionalSchemaClass(object, "schemaClass"); err != nil {
			return nil, err
		}
		if item.TargetSchema, err = optionalString(object, "targetSchemaVersion"); err != nil {
			return nil, err
		}
		if item.Payload, err = parseDescriptor(object["payload"]); err != nil {
			return nil, err
		}
		if item.Resources, err = parseResources(object["resources"]); err != nil {
			return nil, err
		}
		if item.Dependencies, err = parseDependencies(object["dependencies"]); err != nil {
			return nil, err
		}
		if value, ok := object["extensions"]; ok {
			if item.Extensions, err = parseExtensions(value); err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// parseDescriptor checks the shape of a byte descriptor. It checks that:
//   - The object contains exactly mediaType, digest, size, and bundlePath.
//   - String fields are non-empty.
//   - Size has non-negative base-10 integer syntax and fits int64.
//
// Media type, digest, and path syntax are not checked here.
// The declared size and digest are not compared with actual bytes.
func parseDescriptor(raw any) (artifactmodel.Descriptor, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return artifactmodel.Descriptor{}, invalid("descriptor must be object")
	}
	if err := strictjson.RejectUnknownFields(object, "mediaType", "digest", "size", "bundlePath"); err != nil {
		return artifactmodel.Descriptor{}, err
	}
	media, err := requiredString(object, "mediaType")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	digest, err := requiredString(object, "digest")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	path, err := requiredString(object, "bundlePath")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	size, err := number(object, "size")
	if err != nil {
		return artifactmodel.Descriptor{}, err
	}
	return artifactmodel.Descriptor{MediaType: media, Digest: digest, Size: size, BundlePath: path}, nil
}

// parseResources checks resource entry shapes. It checks that:
//   - The input is a bounded array of resource objects.
//   - Each resource has non-empty name and role strings and passes the byte descriptor parsing checks.
//
// Unknown fields are rejected; name uniqueness, role syntax, descriptor integrity, and program bindings are not checked here.
func parseResources(raw any) ([]artifactmodel.Resource, error) {
	values, err := array(raw, "resources")
	if err != nil {
		return nil, err
	}
	result := make([]artifactmodel.Resource, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("resource must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "name", "role", "mediaType", "digest", "size", "bundlePath"); err != nil {
			return nil, err
		}
		name, err := requiredString(object, "name")
		if err != nil {
			return nil, err
		}
		role, err := requiredString(object, "role")
		if err != nil {
			return nil, err
		}
		descriptor, err := parseResourceDescriptor(object)
		if err != nil {
			return nil, err
		}
		result = append(result, artifactmodel.Resource{Name: name, Role: artifactmodel.ResourceRole(role), Descriptor: descriptor})
	}
	return result, nil
}

// parseResourceDescriptor checks a resource's byte descriptor fields using [parseDescriptor].
// Additional resource fields are not checked here.
func parseResourceDescriptor(object map[string]any) (artifactmodel.Descriptor, error) {
	return parseDescriptor(map[string]any{
		"mediaType":  object["mediaType"],
		"digest":     object["digest"],
		"size":       object["size"],
		"bundlePath": object["bundlePath"],
	})
}

// parseDependencies checks dependency entry shapes. It checks that:
//   - The input is a bounded array of objects containing only kind, artifactType, and artifactRevision.
//   - Kinds are schema, context, or source-mapping.
//   - Logical type and revision strings are present and non-empty.
//
// Identifier syntax, uniqueness, ordering, and target selection are not checked here.
func parseDependencies(raw any) ([]artifactmodel.Dependency, error) {
	values, err := array(raw, "dependencies")
	if err != nil {
		return nil, err
	}
	result := make([]artifactmodel.Dependency, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("dependency must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "artifactRevision"); validationErr != nil {
			return nil, validationErr
		}
		item := artifactmodel.Dependency{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.Type, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Revision, err = requiredString(object, "artifactRevision"); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// parseBindings checks schema-context binding shapes. It checks that:
//   - The input is a bounded array of objects containing only schemaRevision and contextRevisions.
//   - Each binding has a non-empty schema revision.
//   - Each context revision array is bounded and non-empty, with non-empty string elements.
//
// Revision syntax, uniqueness, ordering, and selected artifact kinds are not checked here.
func parseBindings(raw any) ([]Binding, error) {
	values, err := array(raw, "schemaContextBindings")
	if err != nil {
		return nil, err
	}
	result := make([]Binding, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("binding must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "schemaRevision", "contextRevisions"); err != nil {
			return nil, err
		}
		schema, err := requiredString(object, "schemaRevision")
		if err != nil {
			return nil, err
		}
		contexts, err := stringsArray(object["contextRevisions"], "contextRevisions")
		if err != nil {
			return nil, err
		}
		if len(contexts) == 0 {
			return nil, invalid("context revisions must not be empty")
		}
		result = append(result, Binding{schema, contexts})
	}
	return result, nil
}

// parseURIs checks URI catalog entry shapes. It checks that:
//   - The input is a bounded array of objects containing uri, artifactRevision, and an optional resource string.
//   - Required fields are present, all present fields are non-empty strings, and no unknown fields occur.
//
// An absent resource selects the primary payload; identifier syntax, uniqueness, and target existence are not checked here.
func parseURIs(raw any) ([]URIEntry, error) {
	values, err := array(raw, "uriCatalog")
	if err != nil {
		return nil, err
	}
	result := make([]URIEntry, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("URI entry must be object")
		}
		if err := strictjson.RejectUnknownFields(object, "uri", "artifactRevision", "resource"); err != nil {
			return nil, err
		}
		uri, err := requiredString(object, "uri")
		if err != nil {
			return nil, err
		}
		revision, err := requiredString(object, "artifactRevision")
		if err != nil {
			return nil, err
		}
		resource, err := optionalString(object, "resource")
		if err != nil {
			return nil, err
		}
		result = append(result, URIEntry{uri, revision, resource})
	}
	return result, nil
}

// parseStorage checks storage catalog entry shapes. It checks that:
//   - The input is a bounded array of objects containing only kind, artifactType, and stores.
//   - Kinds are registered, artifactType is a non-empty string, and stores is a bounded array of non-empty strings.
//
// Store registration, non-empty assignments, schema classification, uniqueness, and ordering are not checked here.
func parseStorage(raw any) ([]StorageEntry, error) {
	values, err := array(raw, "storageCatalog")
	if err != nil {
		return nil, err
	}
	result := make([]StorageEntry, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("storage entry must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "kind", "artifactType", "stores"); validationErr != nil {
			return nil, validationErr
		}
		item := StorageEntry{}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		stores, err := stringsArray(object["stores"], "stores")
		if err != nil {
			return nil, err
		}
		item.Stores = make([]Store, 0, len(stores))
		for _, store := range stores {
			item.Stores = append(item.Stores, Store(store))
		}
		result = append(result, item)
	}
	return result, nil
}

// parseChanges checks the change summary shape. It checks that:
//   - All four change arrays are present and their entries pass the respective change parsing checks.
//   - Compatibility is initial, breaking, additive, or operational.
//   - The optional summary is a non-empty string, and no unknown fields occur.
//
// Ordering, correspondence to the predecessor, aggregate impact, and the schema version increment are not checked here.
func parseChanges(raw any) (*Changes, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("changes must be an object")
	}
	if err := strictjson.RejectUnknownFields(object, "summary", "compatibility", "artifacts", "schemaContextBindings", "uriCatalog", "storageCatalog"); err != nil {
		return nil, err
	}
	changes := &Changes{}
	var err error
	if changes.Summary, err = optionalString(object, "summary"); err != nil {
		return nil, err
	}
	if changes.Compatibility, err = requiredEnum[Compatibility](object, "compatibility"); err != nil {
		return nil, err
	}
	if changes.Artifacts, err = parseArtifactChanges(object["artifacts"]); err != nil {
		return nil, err
	}
	if changes.SchemaContextBindings, err = parseBindingChanges(object["schemaContextBindings"]); err != nil {
		return nil, err
	}
	if changes.URICatalog, err = parseURIChanges(object["uriCatalog"]); err != nil {
		return nil, err
	}
	if changes.StorageCatalog, err = parseStorageChanges(object["storageCatalog"]); err != nil {
		return nil, err
	}
	if changes.Compatibility != CompatibilityInitial && changes.Compatibility != CompatibilityBreaking && changes.Compatibility != CompatibilityAdditive && changes.Compatibility != CompatibilityOperational {
		return nil, invalid("invalid changes compatibility")
	}
	return changes, nil
}

// parseArtifactChanges checks artifact change entries. It checks that:
//   - The array is bounded and entries have registered kinds, valid logical types, and no unknown fields.
//   - Each change passes [validateArtifactChange], including revision selectors and breaking impact for removals.
func parseArtifactChanges(raw any) ([]ArtifactChange, error) {
	values, err := array(raw, "artifact changes")
	if err != nil {
		return nil, err
	}
	result := make([]ArtifactChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("artifact change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "beforeRevision", "afterRevision", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := ArtifactChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.BeforeRevision, err = optionalString(object, "beforeRevision"); err != nil {
			return nil, err
		}
		if item.AfterRevision, err = optionalString(object, "afterRevision"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if err := validateArtifactChange(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// parseBindingChanges checks binding change entries. It checks that:
//   - The bounded array contains only add or remove entries with operation, schemaRevision, contextRevision, and impact fields.
//   - Revisions are non-empty strings, and impacts are breaking, additive, or operational.
//
// Revision syntax, reference resolution, and whether the impact matches the actual change are not checked here.
func parseBindingChanges(raw any) ([]BindingChange, error) {
	values, err := array(raw, "binding changes")
	if err != nil {
		return nil, err
	}
	result := make([]BindingChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("binding change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "schemaRevision", "contextRevision", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := BindingChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.SchemaRevision, err = requiredString(object, "schemaRevision"); err != nil {
			return nil, err
		}
		if item.ContextRevision, err = requiredString(object, "contextRevision"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != OperationAdd && item.Operation != OperationRemove) || !validImpact(item.Impact) {
			return nil, invalid("invalid binding change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseURIChanges checks URI change entries. It checks that:
//   - The array is bounded and entries have absolute document identifiers and no unknown fields.
//   - Present revisions and resource names have valid identifier syntax, and targets pass [validURIChangeShape].
//   - Impacts are breaking, additive, or operational.
//
// Impact accuracy and target existence are not checked here.
func parseURIChanges(raw any) ([]URIChange, error) {
	values, err := array(raw, "URI changes")
	if err != nil {
		return nil, err
	}
	result := make([]URIChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("URI change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "uri", "beforeRevision", "beforeResource", "afterRevision", "afterResource", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := URIChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.URI, err = requiredString(object, "uri"); err != nil {
			return nil, err
		}
		if item.BeforeRevision, err = optionalString(object, "beforeRevision"); err != nil {
			return nil, err
		}
		if item.BeforeResource, err = optionalString(object, "beforeResource"); err != nil {
			return nil, err
		}
		if item.AfterRevision, err = optionalString(object, "afterRevision"); err != nil {
			return nil, err
		}
		if item.AfterResource, err = optionalString(object, "afterResource"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if !artifactmodel.ValidDocumentURI(item.URI) || !validURIChangeShape(item) || !validImpact(item.Impact) || (item.BeforeResource != "" && !artifactmodel.ValidResourceName(item.BeforeResource)) || (item.AfterResource != "" && !artifactmodel.ValidResourceName(item.AfterResource)) {
			return nil, invalid("invalid URI change")
		}
		result = append(result, item)
	}
	return result, nil
}

// validURIChangeShape checks operation-specific URI targets. It checks that:
//   - Add entries have only an after target with a valid revision.
//   - Remove entries have only a before target with a valid revision.
//   - Updates have valid revisions on both sides and change the revision, resource selector, or both.
//
// An empty resource selects the primary payload; resource-name syntax is not checked here.
func validURIChangeShape(item URIChange) bool {
	switch item.Operation {
	case OperationAdd:
		return item.BeforeRevision == "" && item.BeforeResource == "" && artifactmodel.ValidRevision(item.AfterRevision)
	case OperationRemove:
		return artifactmodel.ValidRevision(item.BeforeRevision) && item.AfterRevision == "" && item.AfterResource == ""
	case OperationUpdate:
		return artifactmodel.ValidRevision(item.BeforeRevision) && artifactmodel.ValidRevision(item.AfterRevision) &&
			(item.BeforeRevision != item.AfterRevision || item.BeforeResource != item.AfterResource)
	default:
		return false
	}
}

// parseStorageChanges checks storage change entries. It checks that:
//   - The bounded array contains add or remove entries with registered kinds and stores and no unknown fields.
//   - Logical types are non-empty strings, and impacts are breaking, additive, or operational.
//
// Logical type syntax, schema selection, and whether the impact matches the actual change are not checked here.
func parseStorageChanges(raw any) ([]StorageChange, error) {
	values, err := array(raw, "storage changes")
	if err != nil {
		return nil, err
	}
	result := make([]StorageChange, 0, len(values))
	for _, value := range values {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, invalid("storage change must be object")
		}
		if validationErr := strictjson.RejectUnknownFields(object, "operation", "kind", "artifactType", "store", "impact"); validationErr != nil {
			return nil, validationErr
		}
		item := StorageChange{}
		if item.Operation, err = requiredEnum[Operation](object, "operation"); err != nil {
			return nil, err
		}
		if item.Kind, err = kind(object, "kind"); err != nil {
			return nil, err
		}
		if item.ArtifactType, err = requiredString(object, "artifactType"); err != nil {
			return nil, err
		}
		if item.Store, err = requiredEnum[Store](object, "store"); err != nil {
			return nil, err
		}
		if item.Impact, err = requiredEnum[Impact](object, "impact"); err != nil {
			return nil, err
		}
		if (item.Operation != OperationAdd && item.Operation != OperationRemove) || !validStore(item.Store) || !validImpact(item.Impact) {
			return nil, invalid("invalid storage change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseExtensions checks that extension data is a bounded object with lowercase, dot-separated reverse-domain member names.
// Namespace ownership, extension semantics, and whether extensions preserve core semantics are not checked.
func parseExtensions(raw any) (map[string]any, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("extensions must be object")
	}
	if err := checkCount(len(object)); err != nil {
		return nil, err
	}
	for name := range object {
		if !extensionPattern.MatchString(name) {
			return nil, invalid("invalid extension name")
		}
	}
	return object, nil
}

// requiredString checks that a field is a present, non-empty JSON string without imposing field-specific lexical rules.
func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return value, nil
}

// requiredEnum checks that a field is a present, non-empty JSON string, not membership in its allowed value set.
func requiredEnum[T ~string](object map[string]any, name string) (T, error) {
	value, err := requiredString(object, name)
	return T(value), err
}

// optionalString returns empty for an absent field and rejects present empty or non-string values.
func optionalString(object map[string]any, name string) (string, error) {
	value, ok := object[name]
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return text, nil
}

// array checks that a value is a JSON array with at most schemaartifacts.MaxArrayEntries elements; empty arrays are accepted.
func array(raw any, name string) ([]any, error) {
	value, ok := raw.([]any)
	if !ok {
		return nil, invalid(name + " must be array")
	}
	if err := checkCount(len(value)); err != nil {
		return nil, err
	}
	return value, nil
}

// stringsArray checks for a bounded array of non-empty strings, without imposing lexical rules, uniqueness, or ordering.
func stringsArray(raw any, name string) ([]string, error) {
	values, err := array(raw, name)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, invalid(name + " must contain non-empty strings")
		}
		result = append(result, text)
	}
	return result, nil
}

// kind checks that a field is a present string equal to schema, context, or source-mapping.
func kind(object map[string]any, name string) (artifactmodel.Kind, error) {
	value, err := requiredString(object, name)
	if err != nil {
		return "", err
	}
	kind := artifactmodel.Kind(value)
	if kind != artifactmodel.KindSchema && kind != artifactmodel.KindContext && kind != artifactmodel.KindSourceMapping {
		return "", invalid("invalid artifact kind")
	}
	return kind, nil
}

// optionalSchemaClass checks that a classification is a non-empty string when present; absent classifications are accepted.
// Registered classifications and kind-specific presence requirements are not checked here.
func optionalSchemaClass(object map[string]any, name string) (artifactmodel.SchemaClass, error) {
	value, err := optionalString(object, name)
	if err != nil {
		return "", err
	}
	return artifactmodel.SchemaClass(value), nil
}

// number checks that a field is a present JSON number with non-negative base-10 integer syntax that fits int64.
// The I-JSON safe-integer bound is a prerequisite of strict JSON parsing, not a check performed here.
func number(object map[string]any, name string) (int64, error) {
	raw, ok := object[name]
	if !ok {
		return 0, invalid(name + " is required")
	}
	value, ok := raw.(interface{ String() string })
	if !ok {
		return 0, invalid(name + " must be number")
	}
	number, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || number < 0 {
		return 0, invalid("invalid size")
	}
	return number, nil
}
