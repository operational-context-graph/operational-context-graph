// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package sourcemapping

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/schema-artifacts/strictjson"
)

const DescriptorMediaType = "application/vnd.apeiro.ocg.source-mapping.v1+json"

var errInvalid = errors.New("invalid source-mapping descriptor")

// Descriptor is the parsed source-mapping descriptor format 1.0.0.
type Descriptor struct {
	Schema        string
	FormatVersion string
	ID            string
	SourceSchema  SchemaReference
	TargetSchema  SchemaReference
	Cardinality   string
	Engine        Engine
	ProgramRef    string
	ProgramDigest string
}

// SchemaReference identifies a schema by its immutable revision or release version.
type SchemaReference struct {
	ID       string
	Revision string
	Version  string
}

// Engine is publisher-declared metadata. It does not imply execution support.
type Engine struct {
	Language         string
	LanguageVersion  string
	ExecutionProfile string
}

// Schema is a selected schema revision in one release.
type Schema struct {
	Type     string
	Revision string
	Class    artifact.SchemaClass
}

// Release supplies the selected schemas of one release in a caller-provided lineage.
type Release struct {
	Version string
	Schemas []Schema
}

// Mapping preserves a validated representation of a mapping revision for reuse checks.
type Mapping struct {
	Artifact   artifact.Artifact
	Descriptor Descriptor
}

// ValidationContext supplies the containing release first, followed by its ancestors.
// The caller owns the context and MUST provide a consistent immutable release lineage.
type ValidationContext struct {
	Version        string
	Lineage        []Release
	ReusedMappings []Mapping
}

// Result contains the resolved immutable references. OutputClass is derived from the
// target schema, never from a source-mapping descriptor field.
type Result struct {
	Descriptor  Descriptor
	Source      Schema
	Target      Schema
	OutputClass artifact.SchemaClass
	Program     artifact.Resource
}

// Parse decodes and structurally validates a source-mapping descriptor.
func Parse(data []byte) (Descriptor, error) {
	object, err := strictjson.Object(data)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse descriptor: %w", err)
	}
	if err := strictjson.RejectUnknownFields(object, "$schema", "formatVersion", "id", "sourceSchema", "targetSchema", "cardinality", "engine", "programRef", "programDigest"); err != nil {
		return Descriptor{}, fmt.Errorf("descriptor fields: %w", err)
	}
	descriptor := Descriptor{}
	if descriptor.Schema, err = requiredString(object, "$schema"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.FormatVersion, err = requiredString(object, "formatVersion"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.ID, err = requiredString(object, "id"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.Cardinality, err = requiredString(object, "cardinality"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.ProgramRef, err = requiredString(object, "programRef"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.ProgramDigest, err = requiredString(object, "programDigest"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.SourceSchema, err = parseSchemaReference(object, "sourceSchema"); err != nil {
		return Descriptor{}, err
	}
	if descriptor.TargetSchema, err = parseSchemaReference(object, "targetSchema"); err != nil {
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
	program, err := validateProgram(mapping.Artifact, mapping.Descriptor)
	if err != nil {
		return Result{}, err
	}
	source, err := resolveReference(mapping.Descriptor.SourceSchema, context, "source")
	if err != nil {
		return Result{}, err
	}
	target, err := resolveTarget(mapping, context)
	if err != nil {
		return Result{}, err
	}
	if err := validateDependencies(mapping.Artifact.Dependencies, source, target); err != nil {
		return Result{}, err
	}
	if err := validateReuse(mapping, context.ReusedMappings); err != nil {
		return Result{}, err
	}
	return Result{Descriptor: mapping.Descriptor, Source: source, Target: target, OutputClass: target.Class, Program: program}, nil
}

// ValidateRelease validates all selected mappings and enforces one mapping per source type.
func ValidateRelease(mappings []artifact.Artifact, context ValidationContext) ([]Result, error) {
	results := make([]Result, 0, len(mappings))
	sources := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		result, err := Validate(mapping, context)
		if err != nil {
			return nil, err
		}
		if _, exists := sources[result.Source.Type]; exists {
			return nil, fmt.Errorf("%w: multiple mappings for source schema %q", errInvalid, result.Source.Type)
		}
		sources[result.Source.Type] = struct{}{}
		results = append(results, result)
	}
	return results, nil
}

func requiredString(object map[string]any, name string) (string, error) {
	value, ok := object[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("%w: %s must be a non-empty string", errInvalid, name)
	}
	return value, nil
}

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

func validateDescriptor(descriptor Descriptor) error {
	parsed, err := url.Parse(descriptor.Schema)
	if err != nil || !parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: $schema must be an immutable absolute URI", errInvalid)
	}
	if descriptor.FormatVersion != "1.0.0" || !artifact.ValidVersion(descriptor.FormatVersion) {
		return fmt.Errorf("%w: unsupported format version", errInvalid)
	}
	if !artifact.ValidLogicalType(descriptor.ID) || !validReference(descriptor.SourceSchema) || !validReference(descriptor.TargetSchema) {
		return fmt.Errorf("%w: invalid schema reference", errInvalid)
	}
	if descriptor.Cardinality != "one" && descriptor.Cardinality != "many" {
		return fmt.Errorf("%w: invalid cardinality", errInvalid)
	}
	if len(descriptor.Engine.Language) > artifact.MaxIdentifierBytes || len(descriptor.Engine.ExecutionProfile) > artifact.MaxIdentifierBytes || !artifact.ValidVersion(descriptor.Engine.LanguageVersion) {
		return fmt.Errorf("%w: invalid engine metadata", errInvalid)
	}
	if !artifact.ValidResourceName(descriptor.ProgramRef) || !artifact.ValidDigest(descriptor.ProgramDigest) {
		return fmt.Errorf("%w: invalid program reference", errInvalid)
	}
	return nil
}

func validReference(reference SchemaReference) bool {
	if !artifact.ValidLogicalType(reference.ID) {
		return false
	}
	if reference.Revision != "" {
		return artifact.ValidRevision(reference.Revision) && reference.Version == ""
	}
	return artifact.ValidVersion(reference.Version)
}

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

func validateProgram(mapping artifact.Artifact, descriptor Descriptor) (artifact.Resource, error) {
	if len(mapping.Resources) != 1 {
		return artifact.Resource{}, fmt.Errorf("%w: mappings require exactly one program resource", errInvalid)
	}
	var program *artifact.Resource
	for index := range mapping.Resources {
		resource := &mapping.Resources[index]
		if resource.Role != "program" {
			continue
		}
		if program != nil {
			return artifact.Resource{}, fmt.Errorf("%w: multiple program resources", errInvalid)
		}
		program = resource
	}
	if program == nil {
		return artifact.Resource{}, fmt.Errorf("%w: program resource is required", errInvalid)
	}
	if !artifact.ValidResourceName(program.Name) || program.MediaType == "" {
		return artifact.Resource{}, fmt.Errorf("%w: invalid program resource metadata", errInvalid)
	}
	if err := artifact.ValidateDescriptor(program.Descriptor, program.Bytes); err != nil {
		return artifact.Resource{}, fmt.Errorf("validate program bytes: %w", err)
	}
	if descriptor.ProgramRef != program.Name || descriptor.ProgramDigest != program.Digest {
		return artifact.Resource{}, fmt.Errorf("%w: program binding does not match resource", errInvalid)
	}
	return *program, nil
}

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

func resolveTarget(mapping Mapping, context ValidationContext) (Schema, error) {
	targetReference := mapping.Descriptor.TargetSchema
	if targetReference.Version != "" && targetReference.Version != mapping.Artifact.TargetSchema {
		return Schema{}, fmt.Errorf("%w: target selector version differs from target schema version", errInvalid)
	}
	targetRelease, ok := lineageRelease(context.Lineage, mapping.Artifact.TargetSchema)
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

func canonicalOutputClass(class artifact.SchemaClass) bool {
	return class == artifact.SchemaClassEntity || class == artifact.SchemaClassIdentifier || class == artifact.SchemaClassEdge
}

func validateDependencies(dependencies []artifact.Dependency, source, target Schema) error {
	if len(dependencies) != 2 {
		return fmt.Errorf("%w: mappings require exactly source and target dependencies", errInvalid)
	}
	wanted := map[artifact.Dependency]struct{}{
		{Kind: artifact.KindSchema, Type: source.Type, Revision: source.Revision}: {},
		{Kind: artifact.KindSchema, Type: target.Type, Revision: target.Revision}: {},
	}
	if len(wanted) != len(dependencies) {
		return fmt.Errorf("%w: source and target dependencies must differ", errInvalid)
	}
	for _, dependency := range dependencies {
		if _, ok := wanted[dependency]; !ok {
			return fmt.Errorf("%w: dependency does not match a resolved schema", errInvalid)
		}
	}
	return nil
}

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

func lineageRelease(lineage []Release, version string) (Release, bool) {
	for _, release := range lineage {
		if release.Version == version {
			return release, true
		}
	}
	return Release{}, false
}

func selectedSchema(release Release, artifactType, revision string) (Schema, bool) {
	for _, schema := range release.Schemas {
		if schema.Type == artifactType && schema.Revision == revision {
			return schema, true
		}
	}
	return Schema{}, false
}

func selectedSchemaByType(release Release, artifactType string) (Schema, bool) {
	for _, schema := range release.Schemas {
		if schema.Type == artifactType {
			return schema, true
		}
	}
	return Schema{}, false
}
