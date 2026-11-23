// Current release read snapshot and release lifecycle. The functionality in this file establishes
// the read snapshot of the current schema release and interprets release history.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/model"
)

var (
	// ErrNoActiveRelease means the snapshot has no current activated release.
	ErrNoActiveRelease = errors.New("no active release")
	// ErrNotFound means a requested visible storage record is absent.
	ErrNotFound = errors.New("registry record not found")
	// ErrInconsistentState means authoritative and projected state disagree.
	ErrInconsistentState = errors.New("inconsistent registry state")
)

// Timeouts bound pool acquisition and every database operation in a read snapshot.
type Timeouts struct {
	Acquire           time.Duration
	Query             time.Duration
	Statement         time.Duration
	Lock              time.Duration
	IdleInTransaction time.Duration
}

// Store owns static, fully-qualified read queries. It never exposes a writer.
type Store struct {
	pool     *pgxpool.Pool
	timeouts Timeouts
}

// NewStore creates the read-only storage boundary for a configured pool.
func NewStore(pool *pgxpool.Pool, timeouts Timeouts) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	if err := timeouts.validate(); err != nil {
		return nil, err
	}
	return &Store{pool: pool, timeouts: timeouts}, nil
}

// validate rejects disabled or negative timeouts at the storage boundary.
func (t Timeouts) validate() error {
	if t.Acquire <= 0 || t.Query <= 0 || t.Statement <= 0 || t.Lock <= 0 || t.IdleInTransaction <= 0 {
		return errors.New("all database timeouts must be positive")
	}
	return nil
}

// Read executes fn in one read-only repeatable-read snapshot. It acquires and
// releases its connection before the caller can write a network response.
// fn MUST NOT retain the snapshot beyond the callback.
// The acquisition deadline also bounds the transaction; query time does not restart that budget.
func (s *Store) Read(ctx context.Context, fn func(context.Context, *Snapshot) error) error {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, s.timeouts.Acquire)
	defer cancelAcquire()
	conn, err := s.pool.Acquire(acquireCtx)
	if err != nil {
		return fmt.Errorf("acquire read connection: %w", err)
	}
	defer conn.Release()

	operationCtx, cancelOperation := context.WithTimeout(acquireCtx, s.timeouts.Query)
	defer cancelOperation()
	tx, err := conn.BeginTx(operationCtx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			// Request cancellation must not prevent rollback from returning the connection to a usable state.
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), s.timeouts.Query)
			defer cancelCleanup()
			_ = tx.Rollback(cleanupCtx)
		}
	}()

	queryTimeout := s.timeouts.Query
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		queryTimeout = min(queryTimeout, remaining)
	}
	queryCtx := operationCtx
	if timeoutErr := setLocalTimeouts(queryCtx, tx, s.timeouts.capped(queryTimeout)); timeoutErr != nil {
		return timeoutErr
	}
	current, err := readCurrentState(queryCtx, tx)
	if err != nil {
		return err
	}
	snapshot := &Snapshot{tx: tx, Current: current}
	if err := fn(queryCtx, snapshot); err != nil {
		return err
	}
	if err := tx.Commit(queryCtx); err != nil {
		return fmt.Errorf("commit read transaction: %w", err)
	}
	completed = true
	return nil
}

// capped limits server-side transaction timeouts without changing acquisition or query budgets.
func (t Timeouts) capped(limit time.Duration) Timeouts {
	t.Statement = min(t.Statement, limit)
	t.Lock = min(t.Lock, limit)
	t.IdleInTransaction = min(t.IdleInTransaction, limit)
	return t
}

// setLocalTimeouts applies transaction-local PostgreSQL timeouts so pooled sessions retain no request-specific settings.
func setLocalTimeouts(ctx context.Context, tx pgx.Tx, timeouts Timeouts) error {
	values := []struct {
		name  string
		value time.Duration
	}{
		{"statement_timeout", timeouts.Statement},
		{"lock_timeout", timeouts.Lock},
		{"idle_in_transaction_session_timeout", timeouts.IdleInTransaction},
	}
	for _, setting := range values {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config($1, $2, true)", setting.name, strconv.FormatInt(setting.value.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("set local %s: %w", setting.name, err)
		}
	}
	return nil
}

// readCurrentState resolves the singleton current pointer and maximum activation sequence within one transaction.
// It distinguishes no active release from missing or invalid pointer evidence.
func readCurrentState(ctx context.Context, tx pgx.Tx) (model.CurrentState, error) {
	var schemaVersion *string
	var activationSequence *int64
	var snapshotSequence int64
	err := tx.QueryRow(ctx, `
SELECT state.current_schema_version, release.activated_sequence,
       COALESCE((SELECT MAX(activated_sequence) FROM schema_registry.releases), 0)
FROM schema_registry.registry_state AS state
LEFT JOIN schema_registry.releases AS release
  ON release.schema_version = state.current_schema_version
WHERE state.singleton = true`).Scan(&schemaVersion, &activationSequence, &snapshotSequence)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.CurrentState{}, ErrInconsistentState
		}
		return model.CurrentState{}, fmt.Errorf("read current registry state: %w", err)
	}
	if schemaVersion == nil {
		return model.CurrentState{}, ErrNoActiveRelease
	}
	if activationSequence == nil || *activationSequence <= 0 {
		return model.CurrentState{}, ErrInconsistentState
	}
	return model.CurrentState{SchemaVersion: *schemaVersion, ActivationSequence: *activationSequence, SnapshotSequence: snapshotSequence}, nil
}

// Snapshot exposes repository queries confined to one transaction.
// A snapshot is valid only during Store.Read's callback and MUST NOT serve concurrent queries.
type Snapshot struct {
	tx      pgx.Tx
	Current model.CurrentState
}

// CurrentState returns the pointer and immutable paging ceiling for this read snapshot.
func (s *Snapshot) CurrentState() model.CurrentState { return s.Current }

// CurrentRelease returns the release named by this snapshot's current pointer.
func (s *Snapshot) CurrentRelease(ctx context.Context) (model.Release, error) {
	return s.Release(ctx, s.Current.SchemaVersion)
}

// Release returns one activated release only.
func (s *Snapshot) Release(ctx context.Context, schemaVersion string) (model.Release, error) {
	if err := s.VerifyProjection(ctx, schemaVersion, model.ProjectionRelease); err != nil {
		return model.Release{}, err
	}
	const query = `
SELECT schema_version, manifest_digest, manifest_bytes, previous_schema_version,
	     previous_manifest_digest, source_repository, source_commit, changes,
	     activated_sequence
FROM schema_registry.releases
WHERE schema_version = $1 AND activated_sequence IS NOT NULL`
	release, err := scanRelease(s.tx.QueryRow(ctx, query, schemaVersion))
	if err != nil {
		return model.Release{}, err
	}
	if manifestDigest(release.ManifestBytes) != release.ManifestDigest {
		return model.Release{}, ErrInconsistentState
	}
	changes, err := manifestChanges(release.ManifestBytes)
	if err != nil {
		return model.Release{}, err
	}
	release.Changes = changes
	return release, nil
}

// Releases returns activated releases in immutable activation order.
func (s *Snapshot) Releases(ctx context.Context, beforeSequence *int64, limit int) ([]model.Release, error) {
	return s.ReleasesAt(ctx, beforeSequence, s.Current.SnapshotSequence, limit)
}

// ReleasesAt returns activated releases within a cursor-bound read snapshot.
func (s *Snapshot) ReleasesAt(ctx context.Context, beforeSequence *int64, maxSequence int64, limit int) ([]model.Release, error) {
	if err := s.validateReleaseChain(ctx, maxSequence); err != nil {
		return nil, err
	}
	const query = `
SELECT schema_version, manifest_digest, manifest_bytes, previous_schema_version,
       previous_manifest_digest, source_repository, source_commit, changes,
       activated_sequence
FROM schema_registry.releases
WHERE activated_sequence IS NOT NULL
   AND ($1::bigint IS NULL OR activated_sequence < $1)
	  AND activated_sequence <= $2
ORDER BY activated_sequence DESC
LIMIT $3`
	rows, err := s.tx.Query(ctx, query, beforeSequence, maxSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list activated releases: %w", err)
	}
	defer rows.Close()
	var releaseRows []releaseRow
	for rows.Next() {
		release, err := scanReleaseRow(rows)
		if err != nil {
			return nil, err
		}
		if manifestDigest(release.release.ManifestBytes) != release.release.ManifestDigest {
			return nil, ErrInconsistentState
		}
		releaseRows = append(releaseRows, release)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activated releases: %w", err)
	}
	releases := make([]model.Release, len(releaseRows))
	// Fully consume rows before issuing projection queries on the transaction's single connection.
	for index, release := range releaseRows {
		if err := s.VerifyProjection(ctx, release.release.SchemaVersion, model.ProjectionRelease); err != nil {
			return nil, err
		}
		changes, err := manifestChanges(release.release.ManifestBytes)
		if err != nil {
			return nil, err
		}
		release.release.Changes = changes
		releases[index] = release.release
	}
	return releases, nil
}

// IsAncestor verifies immutable same-chain ancestry using indexed endpoint
// lineage evidence. The importer owns the evidence and the API never changes
// it or walks the intervening predecessor history.
func (s *Snapshot) IsAncestor(ctx context.Context, ancestorVersion, descendantVersion string) (bool, error) {
	const query = `
WITH endpoints AS (
  SELECT
    ancestor.chain_id AS ancestor_chain_id,
    ancestor.chain_position AS ancestor_position,
    ancestor.predecessor_schema_version AS ancestor_predecessor_version,
    ancestor.predecessor_manifest_digest AS ancestor_predecessor_digest,
    ancestor_release.previous_schema_version AS ancestor_release_predecessor_version,
    ancestor_release.previous_manifest_digest AS ancestor_release_predecessor_digest,
    ancestor_predecessor.manifest_digest AS ancestor_predecessor_actual_digest,
    ancestor_predecessor_lineage.chain_id AS ancestor_predecessor_chain_id,
    ancestor_predecessor_lineage.chain_position AS ancestor_predecessor_position,
    descendant.chain_id AS descendant_chain_id,
    descendant.chain_position AS descendant_position,
    descendant.predecessor_schema_version AS descendant_predecessor_version,
    descendant.predecessor_manifest_digest AS descendant_predecessor_digest,
    descendant_release.previous_schema_version AS descendant_release_predecessor_version,
    descendant_release.previous_manifest_digest AS descendant_release_predecessor_digest,
    descendant_predecessor.manifest_digest AS descendant_predecessor_actual_digest,
    descendant_predecessor_lineage.chain_id AS descendant_predecessor_chain_id,
    descendant_predecessor_lineage.chain_position AS descendant_predecessor_position
  FROM schema_registry.release_lineage AS ancestor
  JOIN schema_registry.releases AS ancestor_release
    ON ancestor_release.schema_version = ancestor.schema_version
  JOIN schema_registry.release_lineage AS descendant
    ON descendant.schema_version = $2
  JOIN schema_registry.releases AS descendant_release
    ON descendant_release.schema_version = descendant.schema_version
  LEFT JOIN schema_registry.releases AS ancestor_predecessor
    ON ancestor_predecessor.schema_version = ancestor.predecessor_schema_version
  LEFT JOIN schema_registry.release_lineage AS ancestor_predecessor_lineage
    ON ancestor_predecessor_lineage.schema_version = ancestor.predecessor_schema_version
  LEFT JOIN schema_registry.releases AS descendant_predecessor
    ON descendant_predecessor.schema_version = descendant.predecessor_schema_version
  LEFT JOIN schema_registry.release_lineage AS descendant_predecessor_lineage
    ON descendant_predecessor_lineage.schema_version = descendant.predecessor_schema_version
  WHERE ancestor.schema_version = $1
), validated AS (
  SELECT *,
    COALESCE((ancestor_predecessor_version, ancestor_predecessor_digest)
          IS NOT DISTINCT FROM (ancestor_release_predecessor_version, ancestor_release_predecessor_digest)
      AND (descendant_predecessor_version, descendant_predecessor_digest)
          IS NOT DISTINCT FROM (descendant_release_predecessor_version, descendant_release_predecessor_digest)
      AND (
        (ancestor_position = 1 AND ancestor_predecessor_version IS NULL) OR
        (ancestor_position > 1 AND ancestor_predecessor_version IS NOT NULL AND
         ancestor_predecessor_actual_digest = ancestor_predecessor_digest AND
         ancestor_predecessor_chain_id = ancestor_chain_id AND
         ancestor_predecessor_position = ancestor_position - 1)
      )
      AND (
        (descendant_position = 1 AND descendant_predecessor_version IS NULL) OR
        (descendant_position > 1 AND descendant_predecessor_version IS NOT NULL AND
         descendant_predecessor_actual_digest = descendant_predecessor_digest AND
         descendant_predecessor_chain_id = descendant_chain_id AND
         descendant_predecessor_position = descendant_position - 1)
      ), false) AS valid,
    ancestor_chain_id = descendant_chain_id
      AND ancestor_position < descendant_position
      AS is_ancestor
  FROM endpoints
)

SELECT valid, is_ancestor FROM validated`
	var valid bool
	var isAncestor bool
	if err := s.tx.QueryRow(ctx, query, ancestorVersion, descendantVersion).Scan(&valid, &isAncestor); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrInconsistentState
		}
		return false, fmt.Errorf("verify release ancestry: %w", err)
	}
	if !valid {
		return false, ErrInconsistentState
	}
	return isAncestor, nil
}

// validateReleaseChain checks every activated row through the requested
// snapshot ceiling. It validates sequence continuity and predecessor identity
// without materializing the complete history in the API process.
func (s *Snapshot) validateReleaseChain(ctx context.Context, maxSequence int64) error {
	const query = `
WITH ordered AS (
  SELECT activated_sequence, schema_version, manifest_digest,
         previous_schema_version, previous_manifest_digest,
         LEAD(activated_sequence) OVER (ORDER BY activated_sequence DESC) AS predecessor_sequence,
         LEAD(schema_version) OVER (ORDER BY activated_sequence DESC) AS predecessor_version,
         LEAD(manifest_digest) OVER (ORDER BY activated_sequence DESC) AS predecessor_digest
  FROM schema_registry.releases
  WHERE activated_sequence IS NOT NULL AND activated_sequence <= $1
)
SELECT COUNT(*), COALESCE(BOOL_OR(
  activated_sequence <= 0 OR
  (predecessor_sequence IS NULL AND (
    activated_sequence <> 1 OR previous_schema_version IS NOT NULL OR previous_manifest_digest IS NOT NULL
  )) OR
  (predecessor_sequence IS NOT NULL AND (
    predecessor_sequence <> activated_sequence - 1 OR
    previous_schema_version IS NULL OR previous_manifest_digest IS NULL OR
    previous_schema_version <> predecessor_version OR previous_manifest_digest <> predecessor_digest
  ))
), false)
FROM ordered`
	var count int64
	var invalid bool
	if err := s.tx.QueryRow(ctx, query, maxSequence).Scan(&count, &invalid); err != nil {
		return fmt.Errorf("validate activation chain: %w", err)
	}
	if count == 0 || invalid {
		return ErrInconsistentState
	}
	return nil
}

// scanRelease decodes a single-release query and maps an absent row to ErrNotFound.
func scanRelease(row pgx.Row) (model.Release, error) {
	var release model.Release
	err := row.Scan(
		&release.SchemaVersion, &release.ManifestDigest, &release.ManifestBytes, &release.PreviousSchemaVersion,
		&release.PreviousManifestDigest, &release.SourceRepository, &release.SourceCommit, &release.Changes,
		&release.ActivationSequence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Release{}, ErrNotFound
	}
	if err != nil {
		return model.Release{}, fmt.Errorf("scan release: %w", err)
	}
	return release, nil
}

// releaseRow retains a listed release and its activation sequence for chain validation.
type releaseRow struct {
	release  model.Release
	sequence int64
}

// scanReleaseRow decodes the collection query's release and activation sequence columns.
func scanReleaseRow(row pgx.Row) (releaseRow, error) {
	var result releaseRow
	err := row.Scan(
		&result.release.SchemaVersion, &result.release.ManifestDigest, &result.release.ManifestBytes,
		&result.release.PreviousSchemaVersion, &result.release.PreviousManifestDigest,
		&result.release.SourceRepository, &result.release.SourceCommit, &result.release.Changes,
		&result.sequence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return releaseRow{}, ErrNotFound
	}
	if err != nil {
		return releaseRow{}, fmt.Errorf("scan release row: %w", err)
	}
	result.release.ActivationSequence = result.sequence
	return result, nil
}

// validateReleaseChain checks adjacent descending activations and matching predecessor identities in materialized release rows.
func validateReleaseChain(releases []releaseRow) error {
	for index, release := range releases {
		if release.sequence <= 0 {
			return ErrInconsistentState
		}
		if index == len(releases)-1 {
			continue
		}
		predecessor := releases[index+1]
		if predecessor.sequence != release.sequence-1 ||
			release.release.PreviousSchemaVersion == nil || release.release.PreviousManifestDigest == nil ||
			*release.release.PreviousSchemaVersion != predecessor.release.SchemaVersion || *release.release.PreviousManifestDigest != predecessor.release.ManifestDigest {
			return ErrInconsistentState
		}
	}
	return nil
}

// manifestChanges returns the authoritative stored manifest change summary.
// Callers invoke it only after manifest validation succeeds.
func manifestChanges(bytes []byte) ([]byte, error) {
	var document struct {
		Changes json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(bytes, &document); err != nil {
		return nil, fmt.Errorf("read manifest changes: %w", ErrInconsistentState)
	}
	if len(document.Changes) == 0 {
		return nil, nil
	}
	return append([]byte(nil), document.Changes...), nil
}

// StaticQuery contains an auditable code-owned SQL query. Request values are
// always pgx parameters and never become SQL syntax.
type StaticQuery string

// ValidSort verifies that callers select only documented code-owned orderings.
func ValidSort(value string) bool {
	switch value {
	case "activation-desc", "kind-type", "artifact-type", "revision", "store":
		return true
	default:
		return false
	}
}

// FilterValues rejects values that cannot fit a bounded indexed predicate.
func FilterValues(values ...string) error {
	for _, value := range values {
		if value == "" || len(value) > 8192 || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("invalid bounded filter value")
		}
	}
	return nil
}
