// Package release implements read-only schema release-discovery use cases.
package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/schema-artifacts/manifest"
)

// ErrVersionsNotComparable identifies endpoints that do not form a forward comparison on one release chain.
var ErrVersionsNotComparable = errors.New("versions are not comparable")

// RequestError preserves request-snapshot metadata when an operation fails
// after the current release pointer resolves.
type RequestError struct {
	Current               model.CurrentState
	SelectedSchemaVersion string
	err                   error
}

// Error returns the underlying failure text without adding request metadata.
func (e *RequestError) Error() string { return e.err.Error() }

// Unwrap exposes the original failure for errors.Is and errors.As.
func (e *RequestError) Unwrap() error { return e.err }

// RequestMetadata returns resolved version metadata from an operation error.
func RequestMetadata(err error) (model.CurrentState, string, bool) {
	var requestError *RequestError
	if !errors.As(err, &requestError) {
		return model.CurrentState{}, "", false
	}
	return requestError.Current, requestError.SelectedSchemaVersion, true
}

// withRequestMetadata wraps failures only after the current schema version resolves.
func withRequestMetadata(current model.CurrentState, selected string, err error) error {
	if err == nil || current.SchemaVersion == "" {
		return err
	}
	return &RequestError{Current: current, SelectedSchemaVersion: selected, err: err}
}

// WithRequestMetadata attaches safely resolved request versions to an error.
func WithRequestMetadata(current model.CurrentState, selected string, err error) error {
	return withRequestMetadata(current, selected, err)
}

// Snapshot is the narrow read-only release storage contract.
type Snapshot interface {
	// CurrentState returns the resolved current pointer and activation ceiling for this snapshot.
	CurrentState() model.CurrentState
	// CurrentRelease returns the activated release named by the current pointer.
	CurrentRelease(context.Context) (model.Release, error)
	// Release returns an activated release by schema version.
	Release(context.Context, string) (model.Release, error)
	// ReleasesAt lists descending activations below an optional exclusive position and an inclusive snapshot ceiling.
	ReleasesAt(context.Context, *int64, int64, int) ([]model.Release, error)
	// IsAncestor verifies strict same-chain ancestry using immutable endpoint evidence.
	IsAncestor(context.Context, string, string) (bool, error)
	// Artifact returns an immutable revision reachable from an activated release.
	Artifact(context.Context, string) (model.Artifact, error)
	// ReleaseArtifact returns the exact revision selected by a release, kind, and logical type.
	ReleaseArtifact(context.Context, string, string, string) (model.Artifact, error)
	// ReleaseArtifacts lists filtered selections after an optional kind/type keyset position.
	ReleaseArtifacts(context.Context, string, model.ArtifactFilter, *string, *string, int) ([]model.Artifact, error)
	// ArtifactHistory lists a logical artifact's activated selections within an immutable activation ceiling.
	ArtifactHistory(context.Context, string, string, *int64, int64, int) ([]model.ReleaseArtifact, error)
	// Resources lists named resource metadata without requiring resource content bytes.
	Resources(context.Context, string) ([]model.Resource, error)
	// Resource returns exact content and integrity metadata for one revision-owned resource.
	Resource(context.Context, string, string) (model.Resource, error)
	// Dependencies lists exact revision dependencies for an activated-reachable artifact.
	Dependencies(context.Context, string) ([]model.Dependency, error)
	// ContextBindings lists release-selected context revisions for a schema revision.
	ContextBindings(context.Context, string, string) ([]model.SchemaContextBinding, error)
	// StorageEntries lists the logical stores assigned to a release-selected schema type.
	StorageEntries(context.Context, string, string) ([]model.StorageEntry, error)
	// URIEntry resolves an exact URI within a selected release's catalog.
	URIEntry(context.Context, string, string) (model.URIEntry, error)
	// SourceMapping returns resolved mapping details by logical mapping type within a release.
	SourceMapping(context.Context, string, string) (model.SourceMappingDetail, error)
	// SourceMappingByRevision returns ordered mapping details for an exact release-selected revision.
	SourceMappingByRevision(context.Context, string, string) (model.SourceMappingDetail, error)
}

// Reader provides a repeatable-read snapshot. It has no mutation methods.
type Reader interface {
	// Read runs the callback within one snapshot and releases database resources before returning.
	// The callback MUST NOT retain the snapshot beyond its invocation.
	Read(context.Context, func(context.Context, Snapshot) error) error
}

// Service exposes release discovery without transport dependencies.
type Service struct{ reader Reader }

// New creates a release service backed by the supplied snapshot reader.
func New(reader Reader) *Service { return &Service{reader: reader} }

// Discovery contains the current activated release used by registry discovery.
type Discovery struct{ Current model.Release }

// Discovery resolves current release metadata in one read snapshot.
func (s *Service) Discovery(ctx context.Context) (Discovery, error) {
	var result Discovery
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		release, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		result.Current = release
		return nil
	})
	return result, err
}

// ReleasePage pairs an activation-ordered page with its current pointer and paging ceiling.
type ReleasePage struct {
	Current model.CurrentState
	Items   []model.Release
}

// List returns one immutable activation-order page. The caller supplies the
// cursor-bound snapshot ceiling rather than trusting a later current pointer.
func (s *Service) List(ctx context.Context, before *int64, snapshotSequence *int64, limit int) (ReleasePage, error) {
	var result ReleasePage
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		state := snapshot.CurrentState()
		ceiling := state.SnapshotSequence
		if snapshotSequence != nil {
			// A continuation retains its original ceiling even after newer releases activate.
			ceiling = *snapshotSequence
		}
		items, err := snapshot.ReleasesAt(readCtx, before, ceiling, limit)
		if err != nil {
			return withRequestMetadata(state, "", err)
		}
		state.SnapshotSequence = ceiling
		result.Current = state
		result.Items = items
		return nil
	})
	return result, err
}

// Descriptor pairs a selected release with the current pointer from the same read snapshot.
type Descriptor struct {
	Current model.CurrentState
	Release model.Release
}

// Get returns an activated release after checking its stored manifest's format and schema version.
func (s *Service) Get(ctx context.Context, version string) (Descriptor, error) {
	var result Descriptor
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		state := snapshot.CurrentState()
		release, err := snapshot.Release(readCtx, version)
		if err != nil {
			return withRequestMetadata(state, "", err)
		}
		if parsed, parseErr := manifest.Parse(release.ManifestBytes); parseErr != nil || parsed.SchemaVersion != release.SchemaVersion {
			return withRequestMetadata(state, version, fmt.Errorf("validate stored release manifest: %w", postgres.ErrInconsistentState))
		}
		result = Descriptor{Current: state, Release: release}
		return nil
	})
	return result, err
}

// Compare derives a net change summary from two endpoint manifests. It never
// concatenates intermediate summaries.
func (s *Service) Compare(ctx context.Context, from, to string) (manifest.Changes, model.CurrentState, error) {
	var changes manifest.Changes
	var current model.CurrentState
	err := s.reader.Read(ctx, func(readCtx context.Context, snapshot Snapshot) error {
		_, err := snapshot.CurrentRelease(readCtx)
		if err != nil {
			return withRequestMetadata(snapshot.CurrentState(), "", err)
		}
		current = snapshot.CurrentState()
		before, err := snapshot.Release(readCtx, from)
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		after, err := snapshot.Release(readCtx, to)
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		if before.SchemaVersion == after.SchemaVersion || before.ActivationSequence >= after.ActivationSequence {
			return withRequestMetadata(current, "", ErrVersionsNotComparable)
		}
		isAncestor, err := snapshot.IsAncestor(readCtx, before.SchemaVersion, after.SchemaVersion)
		// Activation order alone cannot prove ancestry when branches have disconnected lineage.
		if err != nil {
			return withRequestMetadata(current, "", err)
		}
		if !isAncestor {
			return withRequestMetadata(current, "", ErrVersionsNotComparable)
		}
		beforeManifest, err := manifest.Parse(before.ManifestBytes)
		if err != nil {
			return withRequestMetadata(current, "", fmt.Errorf("parse stored source manifest: %w", postgres.ErrInconsistentState))
		}
		afterManifest, err := manifest.Parse(after.ManifestBytes)
		if err != nil {
			return withRequestMetadata(current, "", fmt.Errorf("parse stored target manifest: %w", postgres.ErrInconsistentState))
		}
		artifacts := make(map[artifact.Dependency]artifact.Artifact)
		// Endpoint payloads supply compatibility evidence; intermediate change summaries do not determine the net diff.
		for _, entry := range append(append([]artifact.Artifact{}, beforeManifest.Artifacts...), afterManifest.Artifacts...) {
			stored, artifactErr := snapshot.Artifact(readCtx, entry.Revision)
			if artifactErr != nil {
				return withRequestMetadata(current, "", artifactErr)
			}
			bound := entry.Metadata()
			bound.Payload = artifact.Descriptor{MediaType: stored.Payload.MediaType, Digest: stored.Payload.Digest, Size: stored.Payload.Size}
			bound.PayloadBytes = stored.Payload.Bytes
			artifacts[artifact.Dependency{Kind: entry.Kind, Type: entry.Type, Revision: entry.Revision}] = bound
		}
		changes, err = manifest.Diff(beforeManifest, afterManifest, manifest.Context{Artifacts: artifacts})
		return withRequestMetadata(current, "", err)
	})
	return changes, current, err
}
