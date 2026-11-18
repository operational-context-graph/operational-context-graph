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

// catalogBuilder holds documents, URI indexes, and pending uses while validating a release catalog.
// Each builder belongs to one documentCatalog call and does not mutate caller-owned documents.
type catalogBuilder struct {
	// documents indexes parsed documents by byte identity.
	documents map[documentKey]releaseDocument
	// entries includes explicit catalog keys and their encoding aliases.
	entries map[string]documentKey
	// declared contains only explicit manifest keys, excluding generated encoding aliases.
	declared map[string]documentKey
	// catalog contains loader documents, including normalized embedded schemas.
	catalog map[string]any
	// schemas records byte identities traversed as schemas, regardless of media type.
	schemas map[documentKey]bool
	// uses is a work queue that grows as traversal discovers references.
	uses []documentUse
}

// newCatalogBuilder allocates release-local state with capacity for the declared URI catalog.
func newCatalogBuilder(catalogSize int) *catalogBuilder {
	return &catalogBuilder{
		documents: make(map[documentKey]releaseDocument),
		entries:   make(map[string]documentKey, catalogSize),
		declared:  make(map[string]documentKey, catalogSize),
		catalog:   make(map[string]any, catalogSize),
		schemas:   make(map[documentKey]bool),
	}
}

// documentCatalog checks runtime JSON Schema and JSON-LD references against the release's URI catalog.
// For document resolution and dependencies, it checks that:
//
//   - Runtime document references resolve within the catalog, and absolute external document references have explicit catalog keys.
//   - Cross-artifact references have exact kind, logical type, and revision dependencies on the owning artifact.
//   - URI and IRI encoding aliases select the same artifact revision and resource.
//   - Referenced supporting resources are JSON documents, not opaque program resources.
//
// For schema identifiers, dialects, and context references, it checks that:
//
//   - Schema $id values are strings without fragments and identify their owning payload or resource in the catalog.
//   - Recognized standard dialects are Draft 2020-12, and custom dialects resolve through the catalog.
//   - Remote contexts, imports, and term-scoped contexts resolve; vocabulary and term IRIs are not document lookups.
//
// Declared manifest references and selected artifact bytes MUST already be validated.
// Full payload syntax validation is separate; no documents are fetched and caller-owned bytes and metadata remain unchanged.
// The returned catalog records schema use independently of declared media types for historical immutability checks.
// Validation errors wrap [ErrInvalid].
func (m Manifest) documentCatalog(selected map[artifact.Dependency]artifact.Artifact) (*releaseCatalog, error) {
	builder := newCatalogBuilder(len(m.URICatalog))
	if err := builder.loadDocuments(m, selected); err != nil {
		return nil, err
	}
	if err := builder.indexCatalog(m); err != nil {
		return nil, err
	}
	if err := builder.validateUses(); err != nil {
		return nil, err
	}
	return &releaseCatalog{documents: builder.catalog, schemas: builder.schemas}, nil
}

// loadDocuments parses selected payloads and supporting JSON resources, then seeds primary document uses.
// Declared release manifest references and selected artifact bytes MUST already be validated.
func (b *catalogBuilder) loadDocuments(m Manifest, selected map[artifact.Dependency]artifact.Artifact) error {
	for _, a := range m.Artifacts {
		bound := selected[artifact.Dependency{Kind: a.Kind, Type: a.Type, Revision: a.Revision}]
		value, err := strictjson.Parse(bound.PayloadBytes)
		if err != nil {
			return fmt.Errorf("%w: catalog payload JSON: %w", ErrInvalid, err)
		}
		key := documentKey{revision: a.Revision}
		b.documents[key] = releaseDocument{owner: bound, value: value}
		if a.Kind == artifact.KindSchema || a.Kind == artifact.KindContext {
			b.uses = append(b.uses, documentUse{key: key, uri: a.CanonicalURI, context: a.Kind == artifact.KindContext})
		}
		for _, resource := range bound.Resources {
			// Even JSON-typed programs remain opaque.
			// Only supporting document resources enter standards loaders.
			if resource.Role == artifact.ResourceRoleProgram || !jsonMediaType(resource.MediaType) {
				continue
			}
			value, err := strictjson.Parse(resource.Bytes)
			if err != nil {
				return fmt.Errorf("%w: catalog resource JSON: %w", ErrInvalid, err)
			}
			b.documents[documentKey{a.Revision, resource.Name}] = releaseDocument{owner: bound, value: value}
		}
	}
	return nil
}

// indexCatalog registers explicit URI keys and resource uses before adding encoding aliases.
// Documents MUST be loaded first; conflicting aliases return an error wrapping [ErrInvalid].
func (b *catalogBuilder) indexCatalog(m Manifest) error {
	for _, entry := range m.URICatalog {
		key := documentKey{entry.ArtifactRevision, entry.Resource}
		b.entries[entry.URI] = key
		b.declared[entry.URI] = key
		if doc, ok := b.documents[key]; ok {
			b.catalog[entry.URI] = doc.value
			if entry.Resource != "" {
				for _, r := range doc.owner.Resources {
					if r.Name == entry.Resource && (r.MediaType == artifact.SchemaMediaType || r.MediaType == artifact.ContextMediaType) {
						b.uses = append(b.uses, documentUse{key: key, uri: entry.URI, context: r.MediaType == artifact.ContextMediaType})
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
		if other, exists := b.entries[encoded]; exists && other != key {
			return invalid("URI catalog encoding aliases disagree")
		}
		b.entries[encoded] = key
		if doc, ok := b.documents[key]; ok {
			b.catalog[encoded] = doc.value
		}
	}
	return nil
}

// validateUses processes pending and discovered uses in insertion order, stopping at the first error.
// Documents and URI indexes MUST be populated first; each distinct documentUse is processed once.
func (b *catalogBuilder) validateUses() error {
	seen := make(map[documentUse]bool)
	//nolint:intrange // Recheck the queue length because traversal appends uses.
	for index := 0; index < len(b.uses); index++ {
		use := b.uses[index]
		if seen[use] {
			continue
		}
		seen[use] = true
		if !use.context {
			b.schemas[use.key] = true
		}
		if err := b.validateUse(use); err != nil {
			return err
		}
	}
	return nil
}

// validateUse selects a document or pointer target and traverses its schema or context references.
// It prefers normalized catalog documents and preserves the retrieval base for context traversal.
func (b *catalogBuilder) validateUse(use documentUse) error {
	value, base := b.documents[use.key].value, use.uri
	if indexed, ok := b.catalog[use.uri]; ok {
		value = indexed
	}
	if use.pointer != "" {
		var err error
		value, base, err = schemaPointer(value, use.pointer, use.uri)
		if err != nil {
			return err
		}
	}
	// resolve binds reference validation to the current document use.
	resolve := func(reference, base string) error {
		return b.resolveReference(use, reference, base)
	}
	if use.context {
		object, _ := value.(map[string]any)
		return walkContext(object["@context"], use.uri, resolve)
	}
	return walkSchema(value, base, func(node map[string]any, base string) error {
		return b.validateSchemaNode(use, node, base)
	})
}

// resolveReference checks catalog membership, explicit declarations, document eligibility, and exact artifact dependencies.
// It queues referenced uses with their retrieval base, pointer, and traversal language intact.
func (b *catalogBuilder) resolveReference(use documentUse, reference, base string) error {
	uri, err := resolveURI(base, reference)
	if err != nil {
		return err
	}
	target, ok := b.entries[uri]
	if !ok {
		return fmt.Errorf("%w: JSON Schema or JSON-LD reference has no URI catalog entry %q", ErrInvalid, uri)
	}
	parsedRef, _ := url.Parse(reference) // resolveURI already validates this reference.
	if parsedRef.IsAbs() && target != use.key {
		document, _, _ := strings.Cut(reference, "#")
		_, full := b.declared[reference]
		_, documentDeclared := b.declared[document]
		if !full && !documentDeclared {
			return fmt.Errorf("%w: reference has no URI catalog entry %q", ErrInvalid, document)
		}
	}
	if _, ok := b.documents[target]; !ok {
		return invalid("reference target is not a supporting JSON document")
	}
	if target.revision != use.key.revision {
		owner := b.documents[target].owner
		dependency := artifact.Dependency{Kind: owner.Kind, Type: owner.Type, Revision: owner.Revision}
		if !slices.Contains(b.documents[use.key].owner.Dependencies, dependency) {
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
		b.uses = append(b.uses, documentUse{key: target, uri: uri, pointer: pointer, context: use.context})
	}
	return nil
}

// validateSchemaNode checks dialects and identifier ownership, then resolves schema references against the effective base.
// It indexes shallow copies of identified nodes with absolute identifiers, leaving the original nodes unchanged.
func (b *catalogBuilder) validateSchemaNode(use documentUse, node map[string]any, base string) error {
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
		if parsed.IsAbs() && b.declared[id] != use.key {
			return invalid("missing required URI catalog entry or schema identifier ownership mismatch")
		}
		if b.entries[base] != use.key {
			return invalid("missing required URI catalog entry or schema identifier ownership mismatch")
		}
		// An embedded resource keeps its effective absolute ID when retrieved independently by that ID.
		normalized := make(map[string]any, len(node))
		maps.Copy(normalized, node)
		normalized["$id"] = base
		b.catalog[base] = normalized
	}
	for _, keyword := range []string{"$ref", "$dynamicRef", "$recursiveRef"} {
		if reference, ok := node[keyword].(string); ok {
			if err := b.resolveReference(use, reference, base); err != nil {
				return err
			}
		}
	}
	if dialect, ok := node["$schema"].(string); ok && !builtInDialect(dialect) {
		if err := b.resolveReference(use, dialect, base); err != nil {
			return err
		}
	}
	return nil
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
