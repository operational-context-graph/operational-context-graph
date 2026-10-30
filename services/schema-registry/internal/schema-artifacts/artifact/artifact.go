// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package artifact validates immutable Schema Registry artifact descriptors.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"

	"github.com/piprate/json-gold/ld"

	config "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/offline"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
)

const (
	SchemaMediaType  = "application/schema+json"
	ContextMediaType = "application/ld+json;profile=\"http://www.w3.org/ns/json-ld#context\""
)

var (
	logicalTypePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	revisionPattern    = regexp.MustCompile(`^[A-Za-z0-9\-._~]+$`)
	digestPattern      = regexp.MustCompile(`^sha-256:[0-9a-f]{64}$`)
	versionPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// Kind identifies a closed artifact category.
type Kind string

const (
	KindSchema        Kind = "schema"
	KindContext       Kind = "context"
	KindSourceMapping Kind = "source-mapping"
)

// SchemaClass identifies the semantic class of schema artifacts.
type SchemaClass string

const (
	SchemaClassEntity     SchemaClass = "entity-type"
	SchemaClassIdentifier SchemaClass = "identifier-type"
	SchemaClassEdge       SchemaClass = "edge-type"
	SchemaClassSource     SchemaClass = "source-record"
)

// Descriptor identifies exact immutable bytes.
type Descriptor struct {
	MediaType  string
	Digest     string
	Size       int64
	BundlePath string
}

// Resource describes an immutable additional artifact resource.
type Resource struct {
	Name string
	Role string
	Descriptor
	Bytes []byte
}

// Dependency identifies an exact selected artifact revision.
type Dependency struct {
	Kind     Kind
	Type     string
	Revision string
}

// Artifact is a typed immutable artifact revision.
type Artifact struct {
	Kind         Kind
	Type         string
	Revision     string
	CreatedUnder string
	SchemaClass  SchemaClass
	TargetSchema string
	CanonicalURI string
	Payload      Descriptor
	PayloadBytes []byte
	Resources    []Resource
	Dependencies []Dependency
	Extensions   map[string]any
}

// Validate validates lexical fields and exact bytes independent of a release.
func (a Artifact) Validate(registryOrigin string) error {
	if !validKind(a.Kind) || !ValidLogicalType(a.Type) || !ValidRevision(a.Revision) || !ValidVersion(a.CreatedUnder) {
		return errors.New("invalid artifact identity")
	}
	if err := validateKindFields(a); err != nil {
		return err
	}
	if err := ValidateCanonicalURI(a.CanonicalURI, registryOrigin, a.Kind, a.Type, a.Revision); err != nil {
		return err
	}
	if err := ValidateDescriptor(a.Payload, a.PayloadBytes); err != nil {
		return fmt.Errorf("validate payload: %w", err)
	}
	seenResources := make(map[string]struct{}, len(a.Resources))
	for _, resource := range a.Resources {
		if _, exists := seenResources[resource.Name]; exists {
			return fmt.Errorf("duplicate resource name %q", resource.Name)
		}
		seenResources[resource.Name] = struct{}{}
		if !ValidResourceName(resource.Name) || !ValidLogicalType(resource.Role) {
			return fmt.Errorf("invalid resource %q", resource.Name)
		}
		if err := ValidateDescriptor(resource.Descriptor, resource.Bytes); err != nil {
			return fmt.Errorf("validate resource %q: %w", resource.Name, err)
		}
	}
	seenDependencies := make(map[Dependency]struct{}, len(a.Dependencies))
	for _, dependency := range a.Dependencies {
		if !validKind(dependency.Kind) || !ValidLogicalType(dependency.Type) || !ValidRevision(dependency.Revision) {
			return errors.New("invalid artifact dependency")
		}
		if _, exists := seenDependencies[dependency]; exists {
			return errors.New("duplicate artifact dependency")
		}
		seenDependencies[dependency] = struct{}{}
	}
	return nil
}

// ValidatePayloadFormat validates the strict format for schema and context payloads.
func (a Artifact) ValidatePayloadFormat() error {
	return a.ValidatePayloadFormatWithCatalog(nil)
}

// ValidatePayloadFormatWithCatalog validates a payload while allowing references
// only to exact caller-supplied release-catalog documents.
func (a Artifact) ValidatePayloadFormatWithCatalog(catalog map[string]any) error {
	value, err := strictjson.Parse(a.PayloadBytes)
	if err != nil {
		return err
	}
	switch a.Kind {
	case KindSchema:
		if a.Payload.MediaType != SchemaMediaType {
			return fmt.Errorf("schema payload must use %s", SchemaMediaType)
		}
		compiler := offline.NewCatalogJSONSchemaCompiler(catalog)
		if err := compiler.AddResource(a.CanonicalURI, value); err != nil {
			return fmt.Errorf("add schema resource: %w", err)
		}
		if _, err := compiler.Compile(a.CanonicalURI); err != nil {
			return fmt.Errorf("compile JSON Schema: %w", err)
		}
	case KindContext:
		if a.Payload.MediaType != ContextMediaType {
			return fmt.Errorf("context payload must use %s", ContextMediaType)
		}
		object, ok := value.(map[string]any)
		if !ok || object["@context"] == nil {
			return errors.New("JSON-LD context must contain @context")
		}
		options := ld.NewJsonLdOptions("")
		options.DocumentLoader = offline.CatalogJSONLDLoader(catalog)
		if _, err := ld.NewJsonLdProcessor().Expand(value, options); err != nil {
			return fmt.Errorf("validate JSON-LD context: %w", err)
		}
	}
	return nil
}

// ValidateDescriptor verifies descriptor metadata and exact byte integrity.
func ValidateDescriptor(descriptor Descriptor, data []byte) error {
	if descriptor.MediaType == "" || len(descriptor.MediaType) > config.MaxIdentifierBytes || descriptor.BundlePath == "" {
		return errors.New("descriptor requires a bounded media type and bundle path")
	}
	if _, _, err := mime.ParseMediaType(descriptor.MediaType); err != nil {
		return fmt.Errorf("invalid media type: %w", err)
	}
	if !ValidBundlePath(descriptor.BundlePath) || !ValidDigest(descriptor.Digest) || descriptor.Size < 0 || descriptor.Size > strictjson.MaxBytes {
		return errors.New("invalid byte descriptor")
	}
	if len(data) > config.MaxArtifactBytes || int64(len(data)) != descriptor.Size {
		return errors.New("descriptor size does not match bytes")
	}
	digest := sha256.Sum256(data)
	if descriptor.Digest != "sha-256:"+hex.EncodeToString(digest[:]) {
		return errors.New("descriptor digest does not match bytes")
	}
	return nil
}

// ValidLogicalType validates format logical type and role identifiers.
func ValidLogicalType(value string) bool {
	return value != "" && len(value) <= config.MaxIdentifierBytes && logicalTypePattern.MatchString(value)
}

// ValidRevision validates opaque URI-safe artifact revisions.
func ValidRevision(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= config.MaxIdentifierBytes && revisionPattern.MatchString(value)
}

// ValidResourceName validates an RFC 3986 unreserved resource name.
func ValidResourceName(value string) bool { return ValidRevision(value) }

// ValidVersion validates published semantic versions.
func ValidVersion(value string) bool { return versionPattern.MatchString(value) }

// ValidDigest validates the Registry SHA-256 digest spelling.
func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

// ValidBundlePath validates an offline package-relative slash-separated path.
func ValidBundlePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// ValidateCanonicalURI ensures a Registry-owned immutable resource URI.
func ValidateCanonicalURI(value, origin string, kind Kind, artifactType, revision string) error {
	if len(value) == 0 || len(value) > config.MaxURIBytes {
		return errors.New("invalid canonical URI length")
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() {
		return errors.New("canonical URI must be absolute")
	}
	base, err := url.Parse(origin)
	if err != nil || !base.IsAbs() || parsed.Scheme != base.Scheme || parsed.Host != base.Host {
		return errors.New("canonical URI must use the Registry origin")
	}
	expected := "/v1/resources/" + string(kind) + "/" + artifactType + "/" + revision
	if parsed.EscapedPath() != expected || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("canonical URI has an invalid path")
	}
	return nil
}

func validKind(kind Kind) bool {
	return kind == KindSchema || kind == KindContext || kind == KindSourceMapping
}

func validSchemaClass(class SchemaClass) bool {
	return class == SchemaClassEntity || class == SchemaClassIdentifier || class == SchemaClassEdge || class == SchemaClassSource
}

func validateKindFields(a Artifact) error {
	switch a.Kind {
	case KindSchema:
		if !validSchemaClass(a.SchemaClass) || a.TargetSchema != "" {
			return errors.New("schema requires a class and no target schema version")
		}
	case KindContext:
		if a.SchemaClass != "" || a.TargetSchema != "" {
			return errors.New("context cannot declare schema-only fields")
		}
	case KindSourceMapping:
		if a.SchemaClass != "" || !ValidVersion(a.TargetSchema) {
			return errors.New("source mapping requires a target schema version")
		}
	}
	return nil
}
