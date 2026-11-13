package manifest

import (
	"fmt"
	"maps"
	"mime"
	"net/url"
	"slices"
	"strconv"
	"strings"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// documentKey identifies bytes independently of the URI through which a document is requested.
// An empty resource selects the revision's primary payload.
type documentKey struct{ revision, resource string }

// releaseDocument pairs decoded JSON with the owning artifact used to check declared dependencies.
type releaseDocument struct {
	owner artifact.Artifact
	value any
}

// releaseCatalog pairs offline documents with the byte identities traversed as JSON Schemas.
// Schema usage remains significant regardless of the owning artifact kind or declared media type.
type releaseCatalog struct {
	documents map[string]any
	schemas   map[documentKey]bool
}

// documentUse identifies a traversal by byte identity, retrieval base, pointer, and language.
// Different bases may interpret the same bytes differently; each use participates in cycle detection.
type documentUse struct {
	key documentKey
	uri string
	// pointer is a decoded JSON Pointer; an empty value selects the document root.
	pointer string
	// context selects JSON-LD context traversal instead of JSON Schema traversal.
	context bool
}

// documentCatalog builds offline JSON documents while checking ownership and dependencies.
// Declared manifest references and selected artifact bytes MUST already be validated.
// Internal URI spellings do not replace exact manifest keys.
// Program resources remain opaque, even with JSON media types.
// It records schema usage separately from document media types.
// It neither fetches documents nor changes caller-owned bytes or metadata.
// Validation errors wrap [ErrInvalid].
func (m Manifest) documentCatalog(selected map[artifact.Dependency]artifact.Artifact) (*releaseCatalog, error) {
	documents := make(map[documentKey]releaseDocument)
	entries := make(map[string]documentKey, len(m.URICatalog))
	declared := make(map[string]documentKey, len(m.URICatalog))
	catalog := make(map[string]any, len(m.URICatalog))
	schemas := make(map[documentKey]bool)
	var uses []documentUse
	for _, a := range m.Artifacts {
		bound := selected[artifact.Dependency{Kind: a.Kind, Type: a.Type, Revision: a.Revision}]
		value, err := strictjson.Parse(bound.PayloadBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: catalog payload JSON: %w", ErrInvalid, err)
		}
		key := documentKey{revision: a.Revision}
		documents[key] = releaseDocument{owner: bound, value: value}
		if a.Kind == artifact.KindSchema || a.Kind == artifact.KindContext {
			uses = append(uses, documentUse{key: key, uri: a.CanonicalURI, context: a.Kind == artifact.KindContext})
		}
		for _, resource := range bound.Resources {
			// Even JSON-typed programs remain opaque.
			// Only supporting document resources enter standards loaders.
			if resource.Role == artifact.ResourceRoleProgram || !jsonMediaType(resource.MediaType) {
				continue
			}
			value, err := strictjson.Parse(resource.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: catalog resource JSON: %w", ErrInvalid, err)
			}
			documents[documentKey{a.Revision, resource.Name}] = releaseDocument{owner: bound, value: value}
		}
	}
	for _, entry := range m.URICatalog {
		key := documentKey{entry.ArtifactRevision, entry.Resource}
		entries[entry.URI] = key
		declared[entry.URI] = key
		if doc, ok := documents[key]; ok {
			catalog[entry.URI] = doc.value
			if entry.Resource != "" {
				for _, r := range doc.owner.Resources {
					if r.Name == entry.Resource && (r.MediaType == artifact.SchemaMediaType || r.MediaType == artifact.ContextMediaType) {
						uses = append(uses, documentUse{key: key, uri: entry.URI, context: r.MediaType == artifact.ContextMediaType})
					}
				}
			}
		}
	}
	// Standards libraries convert IRIs to URI spellings.
	// Keep manifest keys unchanged while preventing ambiguous lookup.
	for _, entry := range m.URICatalog {
		parsed, _ := url.Parse(entry.URI) // Structural validation already checks this identifier.
		encoded := parsed.String()
		key := documentKey{entry.ArtifactRevision, entry.Resource}
		if other, exists := entries[encoded]; exists && other != key {
			return nil, invalid("URI catalog encoding aliases disagree")
		}
		entries[encoded] = key
		if doc, ok := documents[key]; ok {
			catalog[encoded] = doc.value
		}
	}
	seen := make(map[documentUse]bool)
	for index := 0; index < len(uses); index++ {
		use := uses[index]
		if seen[use] {
			continue
		}
		seen[use] = true
		if !use.context {
			schemas[use.key] = true
		}
		doc := documents[use.key]
		value, base := doc.value, use.uri
		if indexed, ok := catalog[use.uri]; ok {
			value = indexed
		}
		if use.pointer != "" {
			var err error
			value, base, err = schemaPointer(value, use.pointer, use.uri)
			if err != nil {
				return nil, err
			}
		}
		resolve := func(reference, base string) error {
			uri, err := resolveURI(base, reference)
			if err != nil {
				return err
			}
			target, ok := entries[uri]
			if !ok {
				return fmt.Errorf("%w: JSON Schema or JSON-LD reference has no URI catalog entry %q", ErrInvalid, uri)
			}
			parsedRef, _ := url.Parse(reference) // resolveURI already validates this reference.
			if parsedRef.IsAbs() && target != use.key {
				document, _, _ := strings.Cut(reference, "#")
				_, full := declared[reference]
				_, documentDeclared := declared[document]
				if !full && !documentDeclared {
					return fmt.Errorf("%w: reference has no URI catalog entry %q", ErrInvalid, document)
				}
			}
			if _, ok := documents[target]; !ok {
				return invalid("reference target is not a supporting JSON document")
			}
			if target.revision != use.key.revision {
				owner := documents[target].owner
				dependency := artifact.Dependency{Kind: owner.Kind, Type: owner.Type, Revision: owner.Revision}
				if !slices.Contains(doc.owner.Dependencies, dependency) {
					return invalid("parsed reference lacks exact artifact dependency")
				}
			}
			// Revisit explicit pointers and other document bases, but process each use only once.
			parsedBase, _ := url.Parse(base)
			fragment := parsedBase.ResolveReference(parsedRef).Fragment
			pointer := ""
			if strings.HasPrefix(fragment, "/") {
				pointer = fragment
			}
			if target != use.key || uri != use.uri || pointer != "" {
				uses = append(uses, documentUse{key: target, uri: uri, pointer: pointer, context: use.context})
			}
			return nil
		}
		if use.context {
			object, _ := value.(map[string]any)
			if err := walkContext(object["@context"], use.uri, resolve); err != nil {
				return nil, err
			}
			continue
		}
		if err := walkSchema(value, base, func(node map[string]any, base string) error {
			if dialect, ok := node["$schema"].(string); ok && builtInDialect(dialect) && strings.TrimSuffix(dialect, "#") != "https://json-schema.org/draft/2020-12/schema" {
				return invalid("JSON Schema documents must use Draft 2020-12")
			}
			if _, hasID := node["$id"]; hasID {
				id, ok := node["$id"].(string)
				if !ok {
					return invalid("JSON Schema identifier must be a string")
				}
				parsed, err := url.Parse(id)
				if err != nil || parsed.Fragment != "" {
					return invalid("JSON Schema identifier must not contain a fragment")
				}
				if parsed.IsAbs() && declared[id] != use.key {
					return invalid("missing required URI catalog entry or schema identifier ownership mismatch")
				}
				if entries[base] != use.key {
					return invalid("missing required URI catalog entry or schema identifier ownership mismatch")
				}
				// An embedded resource keeps its effective absolute ID when retrieved independently by that ID.
				normalized := make(map[string]any, len(node))
				maps.Copy(normalized, node)
				normalized["$id"] = base
				catalog[base] = normalized
			}
			for _, keyword := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
				if reference, ok := node[keyword].(string); ok {
					if err := resolve(reference, base); err != nil {
						return err
					}
				}
			}
			if dialect, ok := node["$schema"].(string); ok && !builtInDialect(dialect) {
				if err := resolve(dialect, base); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return &releaseCatalog{documents: catalog, schemas: schemas}, nil
}

// schemaPointer returns an explicitly referenced value and its enclosing schema identifier scope.
// The pointer MUST be a nonempty decoded JSON Pointer starting with "/".
// Only known enclosing schema positions contribute identifier scope, not identifiers in literal data.
// Missing targets, invalid indices, and invalid scopes return errors wrapping [ErrInvalid].
func schemaPointer(value any, pointer, base string) (any, string, error) {
	mode := "schema"
	for _, encoded := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch node := value.(type) {
		case map[string]any:
			switch mode {
			case "schema":
				if id, ok := node["$id"].(string); ok {
					var err error
					base, err = resolveURI(base, id)
					if err != nil {
						return nil, "", err
					}
				}
				mode = schemaKeywordMode(part)
			case "map":
				mode = "schema"
			default:
				mode = "literal"
			}
			var exists bool
			value, exists = node[part]
			if !exists {
				return nil, "", invalid("JSON Schema pointer target is missing")
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return nil, "", invalid("JSON Schema pointer index is invalid")
			}
			value = node[index]
			if mode == "array" || mode == "schema" {
				mode = "schema"
			} else {
				mode = "literal"
			}
		default:
			return nil, "", invalid("JSON Schema pointer target is invalid")
		}
	}
	return value, base, nil
}

// schemaKeywordMode classifies schema containment for JSON Pointer traversal.
// It returns "schema", "map", or "array" for recognized keywords and "literal" otherwise.
func schemaKeywordMode(keyword string) string {
	switch keyword {
	case "$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies":
		return "map"
	case "allOf", "anyOf", "oneOf", "prefixItems":
		return "array"
	case "additionalProperties", "unevaluatedProperties", "propertyNames", "items", "additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "contentSchema":
		return "schema"
	default:
		return "literal"
	}
}

// builtInDialect reports whether uri names a standards-library built-in JSON Schema dialect.
// It ignores a trailing empty fragment. A recognized dialect is not necessarily Registry-supported.
func builtInDialect(uri string) bool {
	switch strings.TrimSuffix(uri, "#") {
	case "https://json-schema.org/draft/2020-12/schema", "https://json-schema.org/draft/2019-09/schema",
		"http://json-schema.org/draft-07/schema", "http://json-schema.org/draft-06/schema", "http://json-schema.org/draft-04/schema":
		return true
	default:
		return false
	}
}

// jsonMediaType recognizes JSON media types, including parameters and structured-syntax suffixes.
// It does not inspect or validate the representation's bytes.
func jsonMediaType(value string) bool {
	media, _, err := mime.ParseMediaType(value)
	return err == nil && (media == "application/json" || strings.HasSuffix(media, "+json"))
}

// resolveURI returns the absolute document URI after resolving reference against base and removing
// its fragment.
// It rejects malformed references, nonempty queries, and document portions over the Registry limit.
// Internal URI encoding may exceed the original IRI's byte length.
// It does not retrieve documents or check catalog membership.
// Invalid references return errors wrapping [ErrInvalid].
func resolveURI(base, reference string) (string, error) {
	document, _, _ := strings.Cut(reference, "#")
	if len(document) > schemaartifacts.MaxURIBytes {
		return "", invalid("document reference exceeds Registry URI profile")
	}
	root, err := url.Parse(base)
	if err != nil {
		return "", invalid("invalid reference base")
	}
	relative, err := url.Parse(reference)
	if err != nil {
		return "", invalid("invalid document reference")
	}
	resolved := root.ResolveReference(relative)
	resolved.Fragment, resolved.RawFragment = "", ""
	// Catalog keys already satisfy the byte profile.
	// Their internal URI encoding can exceed their original IRI length.
	if !resolved.IsAbs() || resolved.RawQuery != "" {
		return "", invalid("document reference exceeds Registry URI profile")
	}
	return resolved.String(), nil
}

// walkSchema visits schema nodes with resolved scopes, excluding instance data and annotations.
// The visit callback receives original maps and MAY mutate them; walkSchema does not copy value.
// Traversal stops on the first callback or identifier-resolution error.
// Payload syntax validation remains the caller's responsibility.
func walkSchema(value any, base string, visit func(map[string]any, string) error) error {
	node, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if id, ok := node["$id"].(string); ok {
		var err error
		base, err = resolveURI(base, id)
		if err != nil {
			return err
		}
	}
	if err := visit(node, base); err != nil {
		return err
	}
	for _, keyword := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies"} {
		children, _ := node[keyword].(map[string]any)
		for _, key := range sortedKeys(children) {
			if err := walkSchema(children[key], base, visit); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"additionalProperties", "unevaluatedProperties", "propertyNames", "items", "additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "contentSchema"} {
		if err := walkSchema(node[keyword], base, visit); err != nil {
			return err
		}
	}
	// Retained legacy positions cannot bypass closure checks when a standards library recognizes them.
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"} {
		children, _ := node[keyword].([]any)
		for _, child := range children {
			if err := walkSchema(child, base, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// sortedKeys returns object keys in bytewise lexical order for deterministic document traversal.
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// walkContext visits remote references and imports in arrays and term-scoped contexts.
// The value is an "@context" member, not the surrounding document.
// Vocabulary and term IRIs are not document lookups.
// It passes each reference and its base to reference, stopping on the first callback error.
// This traversal discovers references; it does not validate JSON-LD syntax.
func walkContext(value any, base string, reference func(string, string) error) error {
	switch context := value.(type) {
	case string:
		return reference(context, base)
	case []any:
		for _, item := range context {
			if err := walkContext(item, base, reference); err != nil {
				return err
			}
		}
	case map[string]any:
		if imported, ok := context["@import"].(string); ok {
			if err := reference(imported, base); err != nil {
				return err
			}
		}
		for _, key := range sortedKeys(context) {
			term, _ := context[key].(map[string]any)
			if nested, ok := term["@context"]; ok {
				if err := walkContext(nested, base, reference); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
