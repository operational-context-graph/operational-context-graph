// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package sourcemapping validates opaque program bindings and schema references.
package sourcemapping

import (
	"errors"
	"fmt"
	"reflect"

	schemaartifacts "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/strictjson"
)

// Version identifies a supported source-mapping descriptor format version.
type Version string

// FormatVersion is the supported descriptor format version.
const FormatVersion Version = "1.0.0"

// DescriptorMediaType identifies the version 1 opaque-program mapping descriptor representation.
const DescriptorMediaType = "application/vnd.apeiro.ocg.source-mapping.v1+json"

// errInvalid classifies descriptor, program-binding, and release-context validation failures.
var errInvalid = errors.New("invalid source-mapping descriptor")

// Descriptor is the parsed source-mapping descriptor format 1.0.0.
type Descriptor struct {
	// Schema identifies the descriptor's format schema through an absolute URI.
	Schema string
	// FormatVersion selects the descriptor format independently of schema release versions.
	FormatVersion Version
	// ID identifies the mapping's logical artifact type, independently of its source schema's type.
	ID string
	// SourceSchema selects the input schema by logical type and exactly one revision or release selector.
	SourceSchema SchemaReference
	// Engine preserves opaque publisher-declared metadata shared by all referenced programs.
	Engine Engine
	// Programs binds the optional condition and ordered output transformations to owned resources.
	Programs Programs
}

// ProgramReference binds an opaque resource owned by this mapping revision.
type ProgramReference struct {
	// ProgramRef names a program resource owned by this mapping revision.
	ProgramRef string
	// ProgramDigest binds that resource's exact bytes using the Registry's sha-256: spelling.
	ProgramDigest string
}

// Cardinality identifies the number of records produced by one transformation.
type Cardinality string

const (
	// CardinalityOne declares one output record per applicable input for this transformation.
	CardinalityOne Cardinality = "one"
	// CardinalityMany declares a collection of output records per applicable input for this transformation.
	CardinalityMany Cardinality = "many"
)

// Transformation describes one ordered output program and its canonical schema.
type Transformation struct {
	// ProgramReference pins the opaque program resource that produces this output.
	ProgramReference
	// TargetSchema selects the canonical schema governing this transformation's output.
	TargetSchema SchemaReference
	// Cardinality declares this transformation's output multiplicity without implying program execution.
	Cardinality Cardinality
}

// Programs preserves an optional condition and a non-empty transformation list.
type Programs struct {
	// Condition optionally binds the applicability program; nil means no condition is declared.
	Condition *ProgramReference
	// Transformations preserves the publisher's non-empty ordered list of output program bindings.
	Transformations []Transformation
}

// SchemaReference identifies a schema by its immutable revision or release version.
type SchemaReference struct {
	// ID identifies the referenced schema's logical artifact type.
	ID string
	// Revision pins an exact selected schema revision and excludes Version when present.
	Revision string
	// Version selects the schema through a release in the supplied lineage and excludes Revision when present.
	Version string
}

// Engine is publisher-declared metadata. It does not imply execution support.
type Engine struct {
	// Language names the publisher-declared program language; the Registry does not register or execute it.
	Language string
	// LanguageVersion records the publisher-declared semantic version of that language.
	LanguageVersion string
	// ExecutionProfile names the publisher-declared execution contract without certifying support or behavior.
	ExecutionProfile string
}

// Schema is a selected schema revision in one release.
type Schema struct {
	// Type identifies the selected schema's logical artifact type.
	Type string
	// Revision identifies the selected immutable schema revision.
	Revision string
	// Class distinguishes canonical entity, identifier, and edge schemas from source-record schemas.
	Class artifact.SchemaClass
}

// Release supplies the selected schemas of one release in a caller-provided lineage.
type Release struct {
	// Version identifies this schema release within the caller's validated lineage.
	Version string
	// Schemas contains the exact schema selections available in this release.
	Schemas []Schema
}

// Mapping preserves a validated representation of a mapping revision for reuse checks.
type Mapping struct {
	// Artifact contains revision-bound metadata, exact descriptor bytes, resources, and dependencies.
	Artifact artifact.Artifact
	// Descriptor contains the parsed mapping metadata checked against Artifact.
	Descriptor Descriptor
}

// ValidationContext supplies the containing release first, followed by its ancestors.
// The caller owns the context and MUST provide a consistent immutable release lineage.
type ValidationContext struct {
	// Version identifies the release containing the mapping being validated.
	Version string
	// Lineage lists the containing release first, followed by its ancestors toward the root.
	Lineage []Release
	// ReusedMappings supplies prior validated revisions whose immutable metadata and descriptors must remain unchanged.
	ReusedMappings []Mapping
}

// Result contains resolved references without interpreting any program bytes.
type Result struct {
	// Descriptor preserves the structurally validated mapping descriptor.
	Descriptor Descriptor
	// Source resolves the descriptor's input schema to an exact selection in the containing release.
	Source Schema
	// Condition resolves the optional condition to an owned resource; nil preserves an absent condition.
	Condition *artifact.Resource
	// Transformations preserves descriptor order and resolves each target and owned program resource.
	Transformations []ResolvedTransformation
}

// ResolvedTransformation derives output classification from its canonical target.
type ResolvedTransformation struct {
	// Target identifies the canonical schema selected for this transformation's output.
	Target Schema
	// OutputClass derives output classification from Target rather than publisher-declared program metadata.
	OutputClass artifact.SchemaClass
	// Program contains the exact owned program resource after byte-integrity and binding checks.
	Program artifact.Resource
	// Cardinality preserves the descriptor's per-transformation output multiplicity.
	Cardinality Cardinality
}

// Parse decodes and structurally validates a source-mapping descriptor.
// It does not resolve schema selectors, inspect program bytes, or verify resource ownership and digests.
func Parse(data []byte) (Descriptor, error) {
	object, err := strictjson.Object(data)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse descriptor: %w", err)
	}
	if validationErr := strictjson.RejectUnknownFields(object, "$schema", "formatVersion", "id", "sourceSchema", "engine", "programs"); validationErr != nil {
		return Descriptor{}, fmt.Errorf("descriptor fields: %w", validationErr)
	}
	descriptor := Descriptor{}
	if descriptor.Schema, err = requiredString(object, "$schema"); err != nil {
		return Descriptor{}, err
	}
	version, err := requiredString(object, "formatVersion")
	if err != nil {
		return Descriptor{}, err
	}
	descriptor.FormatVersion = Version(version)
	if descriptor.ID, err = requiredString(object, "id"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.SourceSchema, err = parseSchemaReference(object, "sourceSchema"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.Programs, err = parsePrograms(object["programs"]); err != nil {
		return Descriptor{}, err
	}
	if descriptor.Engine, err = parseEngine(object); err != nil {
		return Descriptor{}, err
	}
	if err := validateDescriptor(descriptor); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

// Validate parses the mapping descriptor and validates it against its release context.
func Validate(mapping artifact.Artifact, context ValidationContext) (Result, error) {
	descriptor, err := Parse(mapping.PayloadBytes)
	if err != nil {
		return Result{}, err
	}
	return ValidateParsed(Mapping{Artifact: mapping, Descriptor: descriptor}, context)
}

// ValidateParsed validates a previously parsed descriptor and its immutable resources.
// Callers MUST supply a descriptor parsed from the artifact's exact payload bytes and a consistent release lineage.
func ValidateParsed(mapping Mapping, context ValidationContext) (Result, error) {
	if err := validateMappingArtifact(mapping.Artifact); err != nil {
		return Result{}, err
	}
	if err := validateDescriptor(mapping.Descriptor); err != nil {
		return Result{}, err
	}
	if mapping.Descriptor.ID != mapping.Artifact.Type {
		return Result{}, fmt.Errorf("%w: descriptor id differs from mapping type", errInvalid)
	}
	if err := validateContext(context); err != nil {
		return Result{}, err
	}
	programs, err := validatePrograms(mapping.Artifact, mapping.Descriptor)
	if err != nil {
		return Result{}, err
	}
	source, err := resolveReference(mapping.Descriptor.SourceSchema, context, "source")
	if err != nil {
		return Result{}, err
	}
	result := Result{Descriptor: mapping.Descriptor, Source: source, Transformations: make([]ResolvedTransformation, 0, len(mapping.Descriptor.Programs.Transformations))}
	if condition := mapping.Descriptor.Programs.Condition; condition != nil {
		resource := programs[condition.ProgramRef]
		result.Condition = &resource
	}
	targets := make([]Schema, 0, len(mapping.Descriptor.Programs.Transformations))
	for _, transformation := range mapping.Descriptor.Programs.Transformations {
		target, err := resolveTarget(mapping.Artifact.TargetSchema, transformation.TargetSchema, context)
		if err != nil {
			return Result{}, err
		}
		targets = append(targets, target)
		result.Transformations = append(result.Transformations, ResolvedTransformation{
			Target: target, OutputClass: target.Class, Program: programs[transformation.ProgramRef], Cardinality: transformation.Cardinality,
		})
	}
	if err := validateDependencies(mapping.Artifact.Dependencies, source, targets, context); err != nil {
		return Result{}, err
	}
	if err := validateReuse(mapping, context.ReusedMappings); err != nil {
		return Result{}, err
	}
	return result, nil
}

// ValidateRelease validates distinct mapping identities, including shared sources.
func ValidateRelease(mappings []artifact.Artifact, context ValidationContext) ([]Result, error) {
	results := make([]Result, 0, len(mappings))
	identities := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		result, err := Validate(mapping, context)
		if err != nil {
			return nil, err
		}
		if _, exists := identities[mapping.Type]; exists {
			return nil, fmt.Errorf("%w: duplicate mapping identity", errInvalid)
		}
		identities[mapping.Type] = struct{}{}
		results = append(results, result)
	}
	return results, nil
}

// parsePrograms decodes an optional condition and a bounded, non-empty ordered transformation list with exact binding shapes.
func parsePrograms(raw any) (Programs, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return Programs{}, fmt.Errorf("%w: programs must be an object", errInvalid)
	}
	if err := strictjson.RejectUnknownFields(object, "condition", "transformations"); err != nil {
		return Programs{}, err
	}
	var result Programs
	if rawCondition, exists := object["condition"]; exists {
		condition, isObject := rawCondition.(map[string]any)
		if !isObject {
			return Programs{}, fmt.Errorf("%w: condition must be an object", errInvalid)
		}
		if err := strictjson.RejectUnknownFields(condition, "programRef", "programDigest"); err != nil {
			return Programs{}, err
		}
		reference, err := parseProgramReference(condition)
		if err != nil {
			return Programs{}, err
		}
		result.Condition = &reference
	}
	transformations, ok := object["transformations"].([]any)
	if !ok || len(transformations) == 0 || len(transformations) > schemaartifacts.MaxArrayEntries {
		return Programs{}, fmt.Errorf("%w: transformations must be a non-empty bounded array", errInvalid)
	}
	result.Transformations = make([]Transformation, 0, len(transformations))
	for _, raw := range transformations {
		entry, ok := raw.(map[string]any)
		if !ok {
			return Programs{}, fmt.Errorf("%w: transformation must be an object", errInvalid)
		}
		if err := strictjson.RejectUnknownFields(entry, "programRef", "programDigest", "targetSchema", "cardinality"); err != nil {
			return Programs{}, err
		}
		reference, err := parseProgramReference(entry)
		if err != nil {
			return Programs{}, err
		}
		target, err := parseSchemaReference(entry, "targetSchema")
		if err != nil {
			return Programs{}, err
		}
		cardinality, err := requiredString(entry, "cardinality")
		if err != nil {
			return Programs{}, err
		}
		result.Transformations = append(result.Transformations, Transformation{ProgramReference: reference, TargetSchema: target, Cardinality: Cardinality(cardinality)})
	}
	return result, nil
}

// parseProgramReference retrieves required resource-name and digest strings; descriptor validation checks their lexical forms.
func parseProgramReference(object map[string]any) (ProgramReference, error) {
	name, err := requiredString(object, "programRef")
	if err != nil {
		return ProgramReference{}, err
	}
	digest, err := requiredString(object, "programDigest")
	if err != nil {
		return ProgramReference{}, err
	}
	return ProgramReference{ProgramRef: name, ProgramDigest: digest}, nil
}

// requiredString retrieves a present, non-empty JSON string and identifies the field in validation errors.
func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("%w: %s must be a non-empty string", errInvalid, name)
	}
	return value, nil
}

// parseSchemaReference requires a logical schema identifier and exactly one non-empty revision or release-version selector.
func parseSchemaReference(object map[string]any, name string) (SchemaReference, error) {
	value, ok := object[name].(map[string]any)
	if !ok {
		return SchemaReference{}, fmt.Errorf("%w: %s must be an object", errInvalid, name)
	}
	if err := strictjson.RejectUnknownFields(value, "id", "revision", "version"); err != nil {
		return SchemaReference{}, fmt.Errorf("%s fields: %w", name, err)
	}
	reference := SchemaReference{}
	var err error
	if reference.ID, err = requiredString(value, "id"); err != nil {
		return SchemaReference{}, fmt.Errorf("%s: %w", name, err)
	}
	if revision, ok := value["revision"]; ok {
		if reference.Revision, ok = revision.(string); !ok || reference.Revision == "" {
			return SchemaReference{}, fmt.Errorf("%w: %s revision must be a non-empty string", errInvalid, name)
		}
	}
	if version, ok := value["version"]; ok {
		if reference.Version, ok = version.(string); !ok || reference.Version == "" {
			return SchemaReference{}, fmt.Errorf("%w: %s version must be a non-empty string", errInvalid, name)
		}
	}
	if (reference.Revision == "") == (reference.Version == "") {
		return SchemaReference{}, fmt.Errorf("%w: %s requires exactly one selector", errInvalid, name)
	}
	return reference, nil
}

// parseEngine retrieves required publisher metadata without interpreting or registering the declared execution environment.
func parseEngine(object map[string]any) (Engine, error) {
	value, ok := object["engine"].(map[string]any)
	if !ok {
		return Engine{}, fmt.Errorf("%w: engine must be an object", errInvalid)
	}
	if err := strictjson.RejectUnknownFields(value, "language", "languageVersion", "executionProfile"); err != nil {
		return Engine{}, fmt.Errorf("engine fields: %w", err)
	}
	engine := Engine{}
	var err error
	if engine.Language, err = requiredString(value, "language"); err != nil {
		return Engine{}, err
	}
	if engine.LanguageVersion, err = requiredString(value, "languageVersion"); err != nil {
		return Engine{}, err
	}
	if engine.ExecutionProfile, err = requiredString(value, "executionProfile"); err != nil {
		return Engine{}, err
	}
	return engine, nil
}

// validateDescriptor checks format declarations, lexical selectors, engine metadata, program references, and per-transformation cardinalities.
func validateDescriptor(descriptor Descriptor) error {
	if !artifact.ValidDocumentURI(descriptor.Schema) {
		return fmt.Errorf("%w: $schema must be an immutable absolute URI", errInvalid)
	}
	if descriptor.FormatVersion != FormatVersion {
		return fmt.Errorf("%w: unsupported format version", errInvalid)
	}
	if !artifact.ValidLogicalType(descriptor.ID) || !validReference(descriptor.SourceSchema) {
		return fmt.Errorf("%w: invalid schema reference", errInvalid)
	}
	if descriptor.Engine.Language == "" || descriptor.Engine.ExecutionProfile == "" || len(descriptor.Engine.Language) > schemaartifacts.MaxIdentifierBytes || len(descriptor.Engine.ExecutionProfile) > schemaartifacts.MaxIdentifierBytes || !artifact.ValidVersion(descriptor.Engine.LanguageVersion) {
		return fmt.Errorf("%w: invalid engine metadata", errInvalid)
	}
	if len(descriptor.Programs.Transformations) == 0 || len(descriptor.Programs.Transformations) > schemaartifacts.MaxArrayEntries {
		return fmt.Errorf("%w: invalid transformation count", errInvalid)
	}
	if condition := descriptor.Programs.Condition; condition != nil && !validProgramReference(*condition) {
		return fmt.Errorf("%w: invalid condition reference", errInvalid)
	}
	for _, transformation := range descriptor.Programs.Transformations {
		if !validProgramReference(transformation.ProgramReference) || !validReference(transformation.TargetSchema) {
			return fmt.Errorf("%w: invalid transformation reference", errInvalid)
		}
		if transformation.Cardinality != CardinalityOne && transformation.Cardinality != CardinalityMany {
			return fmt.Errorf("%w: invalid transformation cardinality", errInvalid)
		}
	}
	return nil
}

// validProgramReference checks resource-name safety and the exact Registry digest spelling without loading the resource.
func validProgramReference(reference ProgramReference) bool {
	return artifact.ValidResourceName(reference.ProgramRef) && artifact.ValidDigest(reference.ProgramDigest)
}

// validReference checks the logical schema type and a mutually exclusive, lexically valid revision or version selector.
func validReference(reference SchemaReference) bool {
	if !artifact.ValidLogicalType(reference.ID) {
		return false
	}
	if reference.Revision != "" {
		return artifact.ValidRevision(reference.Revision) && reference.Version == ""
	}
	return artifact.ValidVersion(reference.Version)
}

// validateMappingArtifact checks mapping identity, target version, descriptor media type, and exact primary-payload byte integrity.
func validateMappingArtifact(mapping artifact.Artifact) error {
	if mapping.Kind != artifact.KindSourceMapping || !artifact.ValidLogicalType(mapping.Type) || !artifact.ValidRevision(mapping.Revision) || !artifact.ValidVersion(mapping.TargetSchema) {
		return fmt.Errorf("%w: invalid source-mapping artifact", errInvalid)
	}
	if mapping.Payload.MediaType != DescriptorMediaType {
		return fmt.Errorf("%w: invalid descriptor media type", errInvalid)
	}
	if err := artifact.ValidateDescriptor(mapping.Payload, mapping.PayloadBytes); err != nil {
		return fmt.Errorf("validate descriptor bytes: %w", err)
	}
	return nil
}

// validateContext requires the containing release first and rejects invalid or repeated lineage versions.
func validateContext(context ValidationContext) error {
	if !artifact.ValidVersion(context.Version) || len(context.Lineage) == 0 || context.Lineage[0].Version != context.Version {
		return fmt.Errorf("%w: invalid containing release context", errInvalid)
	}
	versions := make(map[string]struct{}, len(context.Lineage))
	for _, release := range context.Lineage {
		if !artifact.ValidVersion(release.Version) {
			return fmt.Errorf("%w: invalid lineage version", errInvalid)
		}
		if _, exists := versions[release.Version]; exists {
			return fmt.Errorf("%w: duplicate lineage version", errInvalid)
		}
		versions[release.Version] = struct{}{}
	}
	return nil
}

// validatePrograms verifies every resource's bytes and each declared program's ownership, role, digest, and reference coverage.
// Ancillary resources remain valid without program bindings.
func validatePrograms(mapping artifact.Artifact, descriptor Descriptor) (map[string]artifact.Resource, error) {
	resources := make(map[string]artifact.Resource, len(mapping.Resources))
	for _, resource := range mapping.Resources {
		if _, exists := resources[resource.Name]; exists || !artifact.ValidResourceName(resource.Name) || !artifact.ValidLogicalType(string(resource.Role)) {
			return nil, fmt.Errorf("%w: invalid or duplicate resource", errInvalid)
		}
		if err := artifact.ValidateDescriptor(resource.Descriptor, resource.Bytes); err != nil {
			return nil, fmt.Errorf("validate resource bytes: %w", err)
		}
		resources[resource.Name] = resource
	}
	referenced := make(map[string]bool, len(resources))
	references := make([]ProgramReference, 0, len(descriptor.Programs.Transformations)+1)
	if descriptor.Programs.Condition != nil {
		references = append(references, *descriptor.Programs.Condition)
	}
	for _, transformation := range descriptor.Programs.Transformations {
		references = append(references, transformation.ProgramReference)
	}
	for _, reference := range references {
		resource, ok := resources[reference.ProgramRef]
		if !ok || resource.Role != artifact.ResourceRoleProgram || resource.Digest != reference.ProgramDigest {
			return nil, fmt.Errorf("%w: program binding does not match resource", errInvalid)
		}
		referenced[reference.ProgramRef] = true
	}
	for _, resource := range resources {
		if resource.Role == artifact.ResourceRoleProgram && !referenced[resource.Name] {
			return nil, fmt.Errorf("%w: unreferenced program resource", errInvalid)
		}
	}
	return resources, nil
}

// resolveReference resolves a source selector through the supplied lineage and requires the same revision in the containing release.
// The role argument identifies the reference in validation errors.
func resolveReference(reference SchemaReference, context ValidationContext, role string) (Schema, error) {
	current := context.Lineage[0]
	if reference.Revision != "" {
		schema, ok := selectedSchema(current, reference.ID, reference.Revision)
		if !ok {
			return Schema{}, fmt.Errorf("%w: %s schema revision is not selected", errInvalid, role)
		}
		return schema, nil
	}
	release, ok := lineageRelease(context.Lineage, reference.Version)
	if !ok {
		return Schema{}, fmt.Errorf("%w: %s schema version is not in lineage", errInvalid, role)
	}
	schema, ok := selectedSchemaByType(release, reference.ID)
	if !ok {
		return Schema{}, fmt.Errorf("%w: %s schema version does not select schema", errInvalid, role)
	}
	if _, ok := selectedSchema(current, reference.ID, schema.Revision); !ok {
		return Schema{}, fmt.Errorf("%w: %s schema revision differs in containing release", errInvalid, role)
	}
	return schema, nil
}

// resolveTarget requires a canonical schema selected unchanged by both the declared target release and the containing release.
func resolveTarget(targetVersion string, targetReference SchemaReference, context ValidationContext) (Schema, error) {
	if targetReference.Version != "" && targetReference.Version != targetVersion {
		return Schema{}, fmt.Errorf("%w: target selector version differs from target schema version", errInvalid)
	}
	targetRelease, ok := lineageRelease(context.Lineage, targetVersion)
	if !ok {
		return Schema{}, fmt.Errorf("%w: target schema version is not in lineage", errInvalid)
	}
	target, ok := selectedSchemaByType(targetRelease, targetReference.ID)
	if !ok || !canonicalOutputClass(target.Class) {
		return Schema{}, fmt.Errorf("%w: target must select a canonical schema", errInvalid)
	}
	if targetReference.Revision != "" && targetReference.Revision != target.Revision {
		return Schema{}, fmt.Errorf("%w: target revision differs at target schema version", errInvalid)
	}
	if _, ok := selectedSchema(context.Lineage[0], target.Type, target.Revision); !ok {
		return Schema{}, fmt.Errorf("%w: target schema revision differs in containing release", errInvalid)
	}
	return target, nil
}

// canonicalOutputClass accepts entity, identifier, and edge classes while excluding source-record schemas from canonical outputs.
func canonicalOutputClass(class artifact.SchemaClass) bool {
	return class == artifact.SchemaClassEntity || class == artifact.SchemaClassIdentifier || class == artifact.SchemaClassEdge
}

// validateDependencies requires the resolved source and targets, unique dependency identities, and selected revisions for schema dependencies.
func validateDependencies(dependencies []artifact.Dependency, source Schema, targets []Schema, context ValidationContext) error {
	wanted := map[artifact.Dependency]bool{{Kind: artifact.KindSchema, Type: source.Type, Revision: source.Revision}: true}
	for _, target := range targets {
		wanted[artifact.Dependency{Kind: artifact.KindSchema, Type: target.Type, Revision: target.Revision}] = true
	}
	seen := make(map[artifact.Dependency]bool, len(dependencies))
	for _, dependency := range dependencies {
		if seen[dependency] || !artifact.ValidLogicalType(dependency.Type) || !artifact.ValidRevision(dependency.Revision) {
			return fmt.Errorf("%w: invalid or duplicate dependency", errInvalid)
		}
		seen[dependency] = true
		if dependency.Kind == artifact.KindSchema {
			if _, ok := selectedSchema(context.Lineage[0], dependency.Type, dependency.Revision); !ok {
				return fmt.Errorf("%w: dependency schema is not selected", errInvalid)
			}
		} else if dependency.Kind != artifact.KindContext && dependency.Kind != artifact.KindSourceMapping {
			return fmt.Errorf("%w: unsupported dependency kind", errInvalid)
		}
		delete(wanted, dependency)
	}
	if len(wanted) != 0 {
		return fmt.Errorf("%w: missing resolved schema dependency", errInvalid)
	}
	return nil
}

// validateReuse rejects changed artifact metadata or descriptors when a caller supplies a previously validated matching revision.
func validateReuse(mapping Mapping, reused []Mapping) error {
	for _, previous := range reused {
		if previous.Artifact.Revision != mapping.Artifact.Revision {
			continue
		}
		if previous.Artifact.Kind != artifact.KindSourceMapping || !sameBoundArtifact(previous.Artifact, mapping.Artifact) || !reflect.DeepEqual(previous.Descriptor, mapping.Descriptor) {
			return fmt.Errorf("%w: reused mapping revision changed", errInvalid)
		}
	}
	return nil
}

// sameBoundArtifact compares revision-bound metadata and bytes while ignoring release-specific payload and resource bundle paths.
func sameBoundArtifact(left, right artifact.Artifact) bool {
	left.Payload.BundlePath = ""
	right.Payload.BundlePath = ""
	left.Resources = append([]artifact.Resource(nil), left.Resources...)
	right.Resources = append([]artifact.Resource(nil), right.Resources...)
	for index := range left.Resources {
		left.Resources[index].BundlePath = ""
	}
	for index := range right.Resources {
		right.Resources[index].BundlePath = ""
	}
	return reflect.DeepEqual(left, right)
}

// lineageRelease looks up an exact schema version without traversing or retrieving external release history.
func lineageRelease(lineage []Release, version string) (Release, bool) {
	for _, release := range lineage {
		if release.Version == version {
			return release, true
		}
	}
	return Release{}, false
}

// selectedSchema looks up an exact logical schema type and immutable revision within one supplied release.
func selectedSchema(release Release, artifactType, revision string) (Schema, bool) {
	for _, schema := range release.Schemas {
		if schema.Type == artifactType && schema.Revision == revision {
			return schema, true
		}
	}
	return Schema{}, false
}

// selectedSchemaByType resolves a logical schema type to the revision selected by one supplied release.
func selectedSchemaByType(release Release, artifactType string) (Schema, bool) {
	for _, schema := range release.Schemas {
		if schema.Type == artifactType {
			return schema, true
		}
	}
	return Schema{}, false
}
