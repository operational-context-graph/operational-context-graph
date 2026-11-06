// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"

	"github.com/gowebpki/jcs"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	artifactmodel "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// extensionPattern requires dot-separated namespace labels for publisher extension keys.
var extensionPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// Parse strictly decodes a version 1 manifest and requires submitted bytes to be JCS.
// It checks structure, lexical forms, and ordering, but does not resolve bytes or predecessor-dependent changes.
func Parse(data []byte) (Manifest, error) {
	if len(data) > schemaartifacts.MaxArtifactBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", errInvalid)
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || bytes.HasSuffix(data, []byte("\n")) {
		return Manifest{}, fmt.Errorf("%w: manifest must not contain a byte order mark or trailing newline", errInvalid)
	}
	value, err := strictjson.Object(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
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

// parsePrevious accepts null or an exact predecessor object with valid version and digest spellings.
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

// parseSource decodes the required repository and commit fields while rejecting unknown provenance fields.
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

// parseArtifacts decodes bounded release selections and rejects unknown artifact fields before structural validation.
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

// parseDescriptor decodes byte metadata and a bundle path without loading or hashing the described bytes.
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

// parseResources decodes bounded named-resource metadata without retaining resource bytes.
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

// parseResourceDescriptor extracts only byte-descriptor fields from a resource object containing additional name and role fields.
func parseResourceDescriptor(object map[string]any) (artifactmodel.Descriptor, error) {
	return parseDescriptor(map[string]any{
		"mediaType":  object["mediaType"],
		"digest":     object["digest"],
		"size":       object["size"],
		"bundlePath": object["bundlePath"],
	})
}

// parseDependencies decodes bounded exact-revision references with registered artifact kinds.
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

// parseBindings decodes bounded schema-context associations and requires at least one context per binding.
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

// parseURIs decodes bounded document-resolution entries, preserving an absent resource selector as the primary payload.
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

// parseStorage decodes bounded logical schema-store assignments before registered-store and placement validation.
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

// parseChanges decodes a complete change summary and validates its compatibility value and individual change shapes.
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

// parseArtifactChanges enforces registered kinds, supported impacts, and operation-specific revision fields.
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

// parseBindingChanges accepts bounded add or remove entries with supported impacts and non-empty revision selectors.
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

// parseURIChanges checks absolute identifiers, revision transitions, optional resource names, and supported impacts.
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
		if !artifactmodel.ValidDocumentURI(item.URI) || !validChangeShape(item.Operation, item.BeforeRevision, item.AfterRevision) || !validImpact(item.Impact) || (item.BeforeResource != "" && !artifactmodel.ValidResourceName(item.BeforeResource)) || (item.AfterResource != "" && !artifactmodel.ValidResourceName(item.AfterResource)) {
			return nil, invalid("invalid URI change")
		}
		result = append(result, item)
	}
	return result, nil
}

// parseStorageChanges accepts bounded add or remove entries with registered stores, artifact kinds, and supported impacts.
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

// parseExtensions requires a bounded object whose publisher-defined keys use dot-separated namespace labels.
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

// requiredString retrieves a present, non-empty JSON string without applying field-specific lexical rules.
func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", invalid(name + " must be non-empty string")
	}
	return value, nil
}

// requiredEnum decodes a string; callers validate its field-specific allowed values.
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

// array requires a JSON array within the manifest collection limit and names the field in shape errors.
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

// stringsArray decodes a bounded array of non-empty strings without applying element-specific lexical rules.
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

// kind retrieves a required string and rejects values outside the version 1 artifact-kind registry.
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

// optionalSchemaClass decodes an optional classification; artifact-kind validation checks its allowed values and presence.
func optionalSchemaClass(object map[string]any, name string) (artifactmodel.SchemaClass, error) {
	value, err := optionalString(object, name)
	if err != nil {
		return "", err
	}
	return artifactmodel.SchemaClass(value), nil
}

// number retrieves a required non-negative integer from a strict JSON number representation.
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
