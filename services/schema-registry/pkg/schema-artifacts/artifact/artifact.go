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
	"unicode/utf8"

	"github.com/piprate/json-gold/ld"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/offline"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

const (
	// SchemaMediaType identifies JSON Schema payloads.
	SchemaMediaType = "application/schema+json"
	// ContextMediaType identifies JSON-LD context payloads with the required context profile.
	ContextMediaType = "application/ld+json;profile=\"http://www.w3.org/ns/json-ld#context\""
)

var (
	// logicalTypePattern permits lowercase, hyphen-separated logical types and resource roles.
	logicalTypePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	// revisionPattern permits only RFC 3986 unreserved characters in opaque revision identifiers.
	revisionPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~]+$`)
	// digestPattern requires the Registry's SHA-256 prefix and 64 lowercase hexadecimal digits.
	digestPattern = regexp.MustCompile(`^sha-256:[0-9a-f]{64}$`)
	// versionPattern requires three decimal version components without leading zeroes or suffixes.
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// Kind identifies a closed artifact category.
type Kind string

const (
	// KindSchema identifies a JSON Schema artifact.
	KindSchema Kind = "schema"
	// KindContext identifies a JSON-LD context artifact.
	KindContext Kind = "context"
	// KindSourceMapping identifies an opaque-program mapping descriptor.
	KindSourceMapping Kind = "source-mapping"
)

// SchemaClass identifies the semantic class of schema artifacts.
type SchemaClass string

const (
	// SchemaClassEntity describes canonical entities.
	SchemaClassEntity SchemaClass = "entity-type"
	// SchemaClassIdentifier describes canonical identifier types.
	SchemaClassIdentifier SchemaClass = "identifier-type"
	// SchemaClassEdge describes canonical relationships.
	SchemaClassEdge SchemaClass = "edge-type"
	// SchemaClassSource describes source records without canonical store placement.
	SchemaClassSource SchemaClass = "source-record"
)

// Descriptor identifies exact immutable bytes.
type Descriptor struct {
	// MediaType declares the representation's media type, including any parameters.
	MediaType string
	// Digest binds the exact bytes using the Registry's sha-256: spelling.
	Digest string
	// Size declares the exact uncompressed byte count.
	Size int64
	// BundlePath locates the bytes within an offline release bundle, not within the host filesystem.
	BundlePath string
}

// ResourceRole identifies a resource's purpose. Ancillary roles remain extensible.
type ResourceRole string

// ResourceRoleProgram identifies a descriptor-bound condition or transformation program.
const ResourceRoleProgram ResourceRole = "program"

// Resource describes an immutable additional artifact resource.
type Resource struct {
	// Name identifies this resource uniquely within its owning artifact revision.
	Name string
	// Role declares the resource's purpose; program resources require descriptor bindings.
	Role ResourceRole
	// Descriptor binds the resource's byte metadata and offline bundle location.
	Descriptor
	// Bytes contains the exact resource bytes supplied by the caller.
	Bytes []byte
}

// Dependency identifies an exact selected artifact revision.
type Dependency struct {
	// Kind selects the dependency's artifact category.
	Kind Kind
	// Type selects the dependency's case-sensitive logical type within Kind.
	Type string
	// Revision pins the dependency to one immutable revision.
	Revision string
}

// Artifact describes a typed immutable artifact revision and its caller-supplied bytes.
// Immutability is a validation rule, not a property enforced by the Go value.
type Artifact struct {
	// Kind determines the artifact's format and applicable conditional fields.
	Kind Kind
	// Type identifies the logical artifact within Kind.
	Type string
	// Revision identifies the immutable revision without implying an order.
	Revision string
	// CreatedUnderSchemaVersion records the schema release that first selects this revision.
	CreatedUnderSchemaVersion string
	// SchemaClass classifies schema artifacts and remains empty for other kinds.
	SchemaClass SchemaClass
	// TargetSchema identifies the canonical release targeted by a source mapping; other kinds leave it empty.
	TargetSchema string
	// CanonicalURI identifies the primary payload under the configured Registry origin.
	CanonicalURI string
	// Payload binds the primary payload's byte metadata and bundle location.
	Payload Descriptor
	// PayloadBytes contains the exact primary payload bytes supplied by the caller.
	PayloadBytes []byte
	// Resources contains this revision's additional named byte representations.
	Resources []Resource
	// Dependencies pins external artifact references to exact selected revisions.
	Dependencies []Dependency
	// Extensions preserves publisher metadata bound to this immutable revision.
	Extensions map[string]any
}

// Metadata returns revision metadata without payload or resource bytes.
// It copies resources and represents absent resources as an empty slice.
func (a Artifact) Metadata() Artifact {
	a.PayloadBytes = nil
	resources := make([]Resource, len(a.Resources))
	copy(resources, a.Resources)
	for index := range resources {
		resources[index].Bytes = nil
	}
	a.Resources = resources
	return a
}

// Validate validates lexical fields and exact bytes independent of a release.
// It does not resolve dependencies or validate payload-specific formats.
//
// The registryOrigin supplies the absolute scheme and host required by [Artifact.CanonicalURI].
// Bundle paths are checked lexically; Validate does not read files.
func (a Artifact) Validate(registryOrigin string) error {
	if !validKind(a.Kind) || !ValidLogicalType(a.Type) || !ValidRevision(a.Revision) || !ValidVersion(a.CreatedUnderSchemaVersion) {
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
		if !ValidResourceName(resource.Name) || !ValidLogicalType(string(resource.Role)) {
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

// ValidatePayloadFormat parses [Artifact.PayloadBytes] and checks schema or context format without an external
// document catalog. Source-mapping payloads receive only strict JSON parsing here, not descriptor or program-binding
// validation. Callers MUST separately call [Artifact.Validate] to check identity and byte integrity.
func (a Artifact) ValidatePayloadFormat() error {
	return a.ValidatePayloadFormatWithCatalog(nil)
}

// ValidatePayloadFormatWithCatalog validates a payload using caller-supplied release-catalog documents for
// external references. It does not fetch documents or validate source-mapping program bindings.
//
// The catalog maps exact document identifiers to decoded JSON values; nil denies all catalog lookups.
// The schema compiler can also resolve resources registered locally and its built-in schemas.
// Callers MUST separately call [Artifact.Validate] to check identity and byte integrity.
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
// The data contains the uncompressed representation whose length and SHA-256 digest MUST match descriptor.
// The bundle path identifies a location only; this function never opens it.
func ValidateDescriptor(descriptor Descriptor, data []byte) error {
	if descriptor.MediaType == "" || len(descriptor.MediaType) > schemaartifacts.MaxIdentifierBytes || descriptor.BundlePath == "" {
		return errors.New("descriptor requires a bounded media type and bundle path")
	}
	if _, _, err := mime.ParseMediaType(descriptor.MediaType); err != nil {
		return fmt.Errorf("invalid media type: %w", err)
	}
	if !ValidBundlePath(descriptor.BundlePath) || !ValidDigest(descriptor.Digest) || descriptor.Size < 0 || descriptor.Size > schemaartifacts.MaxArtifactBytes {
		return errors.New("invalid byte descriptor")
	}
	if len(data) > schemaartifacts.MaxArtifactBytes || int64(len(data)) != descriptor.Size {
		return errors.New("descriptor size does not match bytes")
	}
	digest := sha256.Sum256(data)
	if descriptor.Digest != "sha-256:"+hex.EncodeToString(digest[:]) {
		return errors.New("descriptor digest does not match bytes")
	}
	return nil
}

// ValidLogicalType reports whether value is a bounded logical type or resource role.
// It accepts lowercase ASCII letters and digits, with single hyphens separating non-empty segments.
func ValidLogicalType(value string) bool {
	return value != "" && len(value) <= schemaartifacts.MaxIdentifierBytes && logicalTypePattern.MatchString(value)
}

// ValidRevision reports whether value is a bounded, non-empty revision using [RFC 3986] unreserved characters.
// It excludes the path segments "." and ".." and does not interpret revision ordering.
//
// [RFC 3986]: https://tools.ietf.org/html/rfc3986
func ValidRevision(value string) bool {
	return value != "" && value != "." && value != ".." && len(value) <= schemaartifacts.MaxIdentifierBytes && revisionPattern.MatchString(value)
}

// ValidResourceName reports whether value satisfies the same lexical rules as ValidRevision.
func ValidResourceName(value string) bool { return ValidRevision(value) }

// ValidVersion checks the three-component lexical form of published schema versions.
// Components use decimal digits without leading zeroes; prerelease and build suffixes are not accepted.
// It does not bound component sizes or compare versions.
func ValidVersion(value string) bool { return versionPattern.MatchString(value) }

// ValidDigest validates the Registry SHA-256 digest spelling.
// The spelling is "sha-256:" followed by exactly 64 lowercase hexadecimal digits.
func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

// ValidBundlePath validates an offline package-relative slash-separated path.
// It rejects empty, dot, and parent segments, leading slashes, and backslashes.
// It does not inspect filesystem entries or prevent symbolic-link traversal when callers later open files.
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

// ValidDocumentURI accepts bounded absolute URIs or IRIs without query strings or fragments.
// It preserves the exact identifier and never resolves or retrieves it.
func ValidDocumentURI(value string) bool {
	if value == "" || len(value) > schemaartifacts.MaxURIBytes || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.RawQuery == "" && parsed.Fragment == ""
}

// ValidateCanonicalURI ensures a Registry-owned immutable resource URI.
// The value MUST use origin's scheme and host and the exact path '/v1/resources/{kind}/{artifactType}/{revision}'.
// Queries and fragments are not accepted; origin's path is ignored.
// Callers MUST separately validate kind, artifactType, and revision.
func ValidateCanonicalURI(value, origin string, kind Kind, artifactType, revision string) error {
	if len(value) == 0 || len(value) > schemaartifacts.MaxURIBytes {
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
	// Compare escaped paths so alternate percent-encoded spellings cannot alias an immutable resource identifier.
	if parsed.EscapedPath() != expected || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("canonical URI has an invalid path")
	}
	return nil
}

// validKind reports whether kind belongs to the closed version 1 artifact registry.
func validKind(kind Kind) bool {
	return kind == KindSchema || kind == KindContext || kind == KindSourceMapping
}

// validSchemaClass reports whether class is a registered canonical or source-record classification.
func validSchemaClass(class SchemaClass) bool {
	return class == SchemaClassEntity || class == SchemaClassIdentifier || class == SchemaClassEdge || class == SchemaClassSource
}

// validateKindFields enforces schema-class and target-version requirements for each artifact kind.
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
