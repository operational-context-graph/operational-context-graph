package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	usecase "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/artifact"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/httpapi"
	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/introspection/release"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
	v1 "github.com/operational-context-graph/operational-context-graph/services/schema-registry/pkg/api/v1"
)

/*
PostgreSQL request-snapshot contract

The shared readStoreSuite owns PostgreSQL through Testcontainers and testify lifecycle hooks.
Goose applies fresh migrations before each database-backed test.
Suite methods stay serial because they share migrated tables.
Fixtures represent externally supplied read-model state, not release transitions.

The candidate fixture includes optional conditions, shared resources, repeated targets, and mixed transformation targets.
Sixteen GET and HEAD requests overlap after PostgreSQL establishes their snapshots.
Each request uses its own snapshot across discovery, candidate queries, direct packages, or nested package pages.
Each response MUST match its corresponding sequential baseline.
Each request MUST issue one current-pointer query inside one read-only repeatable-read transaction.
The statement trace MUST end with a committed transaction.
The pool MUST retain no acquired connections after all callers finish.

registryRows compares all registry tables, including exact bytes, commitments, visibility, lineage, and PostgreSQL row versions.
The test never changes release state while requests run.
Activation success, conflicts, rollback, and publication atomicity remain outside this reader-only contract.
*/

type readTraceKey struct{}

// requestReadTrace records statements and read-boundary entries for one request.
// Nil barrier channels let sequential baseline requests complete without waiting.
type requestReadTrace struct {
	mu      sync.Mutex
	queries []string
	reads   int
	entered chan<- struct{}
	resume  <-chan struct{}
}

// readQueryTracer records driver statements without changing query arguments or execution.
type readQueryTracer struct{}

func (readQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if trace, ok := ctx.Value(readTraceKey{}).(*requestReadTrace); ok {
		trace.mu.Lock()
		trace.queries = append(trace.queries, strings.TrimSpace(data.SQL))
		trace.mu.Unlock()
	}
	return ctx
}

func (readQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// observedReadBoundary holds requests after PostgreSQL establishes their read
// snapshots. No fixture or release state changes while the requests run.
type observedReadBoundary struct{ release.Reader }

func (r observedReadBoundary) Read(ctx context.Context, fn func(context.Context, release.Snapshot) error) error {
	trace := ctx.Value(readTraceKey{}).(*requestReadTrace)
	trace.mu.Lock()
	trace.reads++
	trace.mu.Unlock()
	return r.Reader.Read(ctx, func(ctx context.Context, snapshot release.Snapshot) error {
		if trace.entered != nil {
			select {
			case trace.entered <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-trace.resume:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return fn(ctx, snapshot)
	})
}

// TestConcurrentRequestsUseReadOnlySnapshots overlaps sixteen requests against complete, pre-populated PostgreSQL candidate fixtures.
// It covers current discovery, candidate filters, direct packages, and both nested package pages.
// Every request must use one read-only repeatable-read transaction and read the current pointer once.
// Full responses match sequential baselines, and all registry rows retain their values and row versions.
func (suite *readStoreSuite) TestConcurrentRequestsUseReadOnlySnapshots() {
	t := suite.T()
	fixture := candidateFixture(t)
	value, err := loadValidatedFixture(t, suite.Context, suite.Database, fixture)
	require.NoError(t, err)
	require.NoError(t, prepopulateReadModelState(suite.Context, suite.Database, fixture.version, map[string]int64{fixture.version: 1}))
	before := suite.registryRows()

	config, err := pgxpool.ParseConfig(suite.DatabaseURL)
	require.NoError(t, err)
	config.MaxConns = 20
	config.ConnConfig.Tracer = readQueryTracer{}
	pool, err := pgxpool.NewWithConfig(suite.Context, config)
	require.NoError(t, err)
	defer pool.Close()
	store, err := storage.NewStore(pool, storage.Timeouts{Acquire: 15 * time.Second, Query: 15 * time.Second, Statement: 15 * time.Second, Lock: 15 * time.Second, IdleInTransaction: 15 * time.Second})
	require.NoError(t, err)
	reader := observedReadBoundary{release.NewPostgresReader(store)}
	server := httpapi.NewWithServices(introspection.Config{
		Database:  introspection.DatabaseConfig{QueryTimeout: 15 * time.Second},
		Admission: introspection.AdmissionConfig{MaxInFlight: 20},
	}, nil, release.New(reader), usecase.New(reader))
	handler := server.Handler()
	request := func(ctx context.Context, method, path string, trace *requestReadTrace) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil).WithContext(context.WithValue(ctx, readTraceKey{}, trace))
		handler.ServeHTTP(response, req)
		return response
	}

	const base = "/v1/releases/8.0.0"
	const packages = base + "/schemas/candidate-source/source-mappings/packages?limit=1"
	paths := []string{
		"/v1/registry",
		base + "/source-mappings?sourceArtifactType=candidate-source&limit=1",
		base + "/source-mappings?targetArtifactType=candidate-entity",
		base + "/schemas/candidate-source/source-mappings?limit=1",
		base + "/source-mappings/candidate-mapping-0/package",
		base + "/source-mappings/candidate-mapping-1/package",
		packages,
	}
	first := request(suite.Context, http.MethodGet, packages, &requestReadTrace{})
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var page v1.Page[v1.SourceMappingPackage]
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextCursor)
	require.Equal(t, "candidate-mapping-0", page.Items[0].Mapping.ArtifactType)
	require.Len(t, page.Items[0].Mapping.Programs.Transformations, 3)
	require.Equal(t, "candidate-edge", page.Items[0].Mapping.Programs.Transformations[1].TargetSchema.ArtifactType)
	require.NotNil(t, page.Items[0].Mapping.Programs.Condition)
	require.Len(t, page.Items[0].Schemas, 3)
	require.Len(t, page.Items[0].Programs, 2)
	paths = append(paths, packages+"&cursor="+url.QueryEscape(*page.NextCursor))

	expected := make(map[string]*httptest.ResponseRecorder, len(paths))
	for _, path := range paths {
		expected[path] = request(suite.Context, http.MethodGet, path, &requestReadTrace{})
		require.Equal(t, http.StatusOK, expected[path].Code, expected[path].Body.String())
		require.Equal(t, fixture.version, expected[path].Header().Get("Schema-Version"))
		require.Equal(t, "no-cache", expected[path].Header().Get("Cache-Control"))
		require.NotEmpty(t, expected[path].Header().Get("ETag"))
		if path != "/v1/registry" {
			require.Equal(t, fixture.version, expected[path].Header().Get("Selected-Schema-Version"))
		}
	}
	var registry v1.Registry
	require.NoError(t, json.Unmarshal(expected["/v1/registry"].Body.Bytes(), &registry))
	require.Equal(t, fixture.version, registry.SchemaVersion)
	require.Equal(t, fixtureDigest(value.Raw), registry.ManifestDigest)
	page = v1.Page[v1.SourceMappingPackage]{}
	require.NoError(t, json.Unmarshal(expected[paths[len(paths)-1]].Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, "candidate-mapping-1", page.Items[0].Mapping.ArtifactType)
	require.Nil(t, page.Items[0].Mapping.Programs.Condition)
	require.Nil(t, page.NextCursor)

	const callers = 16
	// The barrier holds established snapshots, not connections awaiting pool acquisition.
	entered, resume := make(chan struct{}, callers), make(chan struct{})
	ctx, cancel := context.WithTimeout(suite.Context, 15*time.Second)
	defer cancel()
	type result struct {
		path, method string
		response     *httptest.ResponseRecorder
		trace        *requestReadTrace
	}
	results := make(chan result, callers)
	var group sync.WaitGroup
	for index := range callers {
		path, method := paths[index%len(paths)], http.MethodGet
		if index >= len(paths) {
			method = http.MethodHead
		}
		group.Go(func() {
			trace := &requestReadTrace{entered: entered, resume: resume}
			results <- result{path: path, method: method, response: request(ctx, method, path, trace), trace: trace}
		})
	}
	for range callers {
		select {
		case <-entered:
		case <-ctx.Done():
			cancel()
			group.Wait()
			require.FailNow(t, "concurrent requests did not establish all snapshots before the deadline")
		}
	}
	close(resume)
	group.Wait()
	close(results)
	// Compare only after every worker finishes, so assertions cannot interrupt sibling cleanup.
	for result := range results {
		require.Equal(t, http.StatusOK, result.response.Code, result.path, result.response.Body.String())
		require.Equal(t, expected[result.path].Header(), result.response.Header(), result.path)
		if result.method == http.MethodHead {
			require.Empty(t, result.response.Body.Bytes())
		} else {
			require.Equal(t, expected[result.path].Body.Bytes(), result.response.Body.Bytes(), result.path)
		}
		require.Equal(t, 1, result.trace.reads, result.path)
		queries := result.trace.queries
		require.NotEmpty(t, queries)
		require.Equal(t, "begin isolation level repeatable read read only", queries[0])
		require.Equal(t, "commit", queries[len(queries)-1])
		pointerReads := 0
		for _, query := range queries[1 : len(queries)-1] {
			require.True(t, strings.HasPrefix(query, "SELECT") || strings.HasPrefix(query, "WITH"), "unexpected statement: %s", query)
			if strings.Contains(query, "FROM schema_registry.registry_state AS state") {
				pointerReads++
			}
		}
		require.Equal(t, 1, pointerReads, result.path)
	}
	require.Zero(t, pool.Stat().AcquiredConns())
	require.Equal(t, before, suite.registryRows(), "requests must not change any registry row")
}

// registryRows compares every registry table, including visibility, lineage,
// commitments, exact bytes, and PostgreSQL row versions, before and after reads.
func (suite *readStoreSuite) registryRows() map[string]string {
	t := suite.T()
	t.Helper()
	rows, err := suite.Database.QueryContext(suite.Context, `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'schema_registry' ORDER BY tablename`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.NotEmpty(t, tables)
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		var value string
		// Identifiers come from PostgreSQL's catalog and use driver quoting.
		query := `SELECT COALESCE(jsonb_agg(to_jsonb(row) || jsonb_build_object('_xmin', row.xmin::text) ORDER BY to_jsonb(row)::text), '[]'::jsonb)::text FROM ` + pgx.Identifier{"schema_registry", table}.Sanitize() + ` AS row`
		require.NoError(t, suite.Database.QueryRowContext(suite.Context, query).Scan(&value))
		result[table] = value
	}
	return result
}
