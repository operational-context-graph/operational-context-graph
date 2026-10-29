// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

//nolint:gochecknoglobals // test-package-level keys shared across test functions.
var (
	srcKey   = ingest.SchemaKey{LogicalArtifactType: "ocg.src.log", Version: "1.0.0"}
	canonKey = ingest.SchemaKey{LogicalArtifactType: "ocg.telemetry.log", Version: "2.4.0"}
)

const schemaSrc = `{
  "type": "object",
  "properties": {"name": {"type": "string"}},
  "required": ["name"]
}`

func mustSchema(t *testing.T) ingest.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaSrc))
	require.NoError(t, err)
	require.NoError(t, c.AddResource("mem://schema.json", doc))
	sch, err := c.Compile("mem://schema.json")
	require.NoError(t, err)
	return sch
}

func conforming() ingest.IngestRecord {
	return ingest.IngestRecord{Schema: srcKey, Payload: []byte(`{"name":"ok"}`), ContentType: "application/json"}
}

func nonconforming() ingest.IngestRecord {
	return ingest.IngestRecord{Schema: srcKey, Payload: []byte(`{"name":123}`)}
}

func normalized() ingest.IngestRecord {
	return ingest.IngestRecord{Schema: canonKey, Payload: []byte(`{"name":"ok"}`), ContentType: "application/json"}
}

// stubAcceptPath wires the full happy path (schema -> mapping -> normalize ->
// target store) for records keyed by srcKey. Call counts are not constrained so
// it works for single and concurrent batches alike.
func stubAcceptPath(reg *SchemaRegistry, eng *Normalizer, sch ingest.Schema) {
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(sch, nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, nil)
	eng.EXPECT().Normalize(mock.Anything, mock.Anything, mock.Anything).Return([]ingest.IngestRecord{normalized()}, nil)
	reg.EXPECT().TargetStore(mock.Anything, canonKey).Return(ingest.StoreID("telemetry"), nil)
}

// --- Group 9: happy path -----------------------------------------------------

func TestPipeline_Ingest_AllAccepted(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.MatchedBy(func(rows []ingest.OutboxRecord) bool {
		return len(rows) == 3
	})).Return(nil).Once()

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming(), conforming(), conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 3, Rejected: 0}, res)
}

func TestPipeline_Ingest_MixedBatch(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.MatchedBy(func(rows []ingest.OutboxRecord) bool {
		return len(rows) == 2
	})).Return(nil).Once()

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming(), nonconforming(), conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 2, Rejected: 1}, res)
}

func TestPipeline_Ingest_AllRejected(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(nil, ingest.ErrNotFound)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming(), conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 2}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

// --- Group 10: boundary inputs ----------------------------------------------

func TestPipeline_Ingest_EmptyBatch(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{}, res)
	reg.AssertNotCalled(t, "Schema", mock.Anything, mock.Anything)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_NilBatch(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), nil)

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{}, res)
}

func TestPipeline_Ingest_SingleConformingRecord(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.MatchedBy(func(rows []ingest.OutboxRecord) bool {
		return len(rows) == 1
	})).Return(nil).Once()

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 1, Rejected: 0}, res)
}

func TestPipeline_Ingest_SingleRejectedRecord(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{nonconforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 1}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_EmptyPayload(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)

	rec := ingest.IngestRecord{Schema: srcKey, Payload: []byte{}}
	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{rec})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 1}, res)
}

// --- Group 11: error paths --------------------------------------------------

func TestPipeline_Ingest_UnregisteredSchemaType(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)

	badKey := ingest.SchemaKey{LogicalArtifactType: "ocg.unknown", Version: "9.9.9"}
	reg.EXPECT().Schema(mock.Anything, badKey).Return(nil, ingest.ErrNotFound)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.MatchedBy(func(rows []ingest.OutboxRecord) bool {
		return len(rows) == 1
	})).Return(nil).Once()

	bad := ingest.IngestRecord{Schema: badKey, Payload: []byte(`{"name":"x"}`)}
	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{bad, conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 1, Rejected: 1}, res)
}

func TestPipeline_Ingest_SchemaRegistryUnavailable(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)

	boom := errors.New("dial tcp: connection refused")
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(nil, boom)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, err, ingest.ErrUnavailable)
	assert.Equal(t, ingest.IngestResult{}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_SourceMappingsNotFound(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, ingest.ErrNotFound)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 1}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_SourceMappingsUnavailable(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	boom := errors.New("registry 503")
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, boom)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, err, ingest.ErrUnavailable)
	assert.Equal(t, ingest.IngestResult{}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_NormalizationError(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, nil)
	eng.EXPECT().Normalize(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("cannot map"))

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 1}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_TargetStoreNotFound(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, nil)
	eng.EXPECT().Normalize(mock.Anything, mock.Anything, mock.Anything).Return([]ingest.IngestRecord{normalized()}, nil)
	reg.EXPECT().TargetStore(mock.Anything, canonKey).Return(ingest.StoreID(""), ingest.ErrNotFound)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.NoError(t, err)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 1}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_TargetStoreUnavailable(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	boom := errors.New("registry timeout")
	reg.EXPECT().Schema(mock.Anything, srcKey).Return(mustSchema(t), nil)
	reg.EXPECT().SourceMappings(mock.Anything, srcKey).Return(ingest.SourceMapping{}, nil)
	eng.EXPECT().Normalize(mock.Anything, mock.Anything, mock.Anything).Return([]ingest.IngestRecord{normalized()}, nil)
	reg.EXPECT().TargetStore(mock.Anything, canonKey).Return(ingest.StoreID(""), boom)

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, err, ingest.ErrUnavailable)
	assert.Equal(t, ingest.IngestResult{}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

func TestPipeline_Ingest_OutboxWriteFailure(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	boom := errors.New("outbox down")
	out.EXPECT().Write(mock.Anything, mock.Anything).Return(boom).Once()

	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming()})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, err, ingest.ErrUnavailable)
	assert.Equal(t, ingest.IngestResult{}, res)
}

func TestPipeline_Ingest_RegistryUnavailableAbortsBeforeOutbox(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)

	boom := errors.New("registry unavailable")
	// The first record would be accepted; a later record's Schema lookup fails
	// transiently, so the whole batch must abort before any outbox write.
	lateKey := ingest.SchemaKey{LogicalArtifactType: "ocg.src.metric", Version: "1.0.0"}
	stubAcceptPath(reg, eng, mustSchema(t))
	reg.EXPECT().Schema(mock.Anything, lateKey).Return(nil, boom)

	late := ingest.IngestRecord{Schema: lateKey, Payload: []byte(`{"name":"x"}`)}
	p := ingest.NewPipeline(reg, eng, out)
	res, err := p.Ingest(context.Background(), []ingest.IngestRecord{conforming(), late})

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, ingest.IngestResult{}, res)
	out.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
}

// --- Group 12: concurrency --------------------------------------------------

func TestPipeline_Ingest_ConcurrentBatches(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.Anything).Return(nil)

	p := ingest.NewPipeline(reg, eng, out)

	const goroutines = 8
	const perBatch = 4
	batch := make([]ingest.IngestRecord, perBatch)
	for i := range batch {
		batch[i] = conforming()
	}

	var wg sync.WaitGroup
	results := make([]ingest.IngestResult, goroutines)
	errs := make([]error, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = p.Ingest(context.Background(), batch)
		}(g)
	}
	wg.Wait()

	for g := range goroutines {
		require.NoError(t, errs[g])
		assert.Equal(t, ingest.IngestResult{Accepted: perBatch, Rejected: 0}, results[g])
	}
}

func TestPipeline_Ingest_ConcurrentMixedBatches(t *testing.T) {
	reg := NewSchemaRegistry(t)
	eng := NewNormalizer(t)
	out := NewOutboxWriter(t)
	stubAcceptPath(reg, eng, mustSchema(t))
	out.EXPECT().Write(mock.Anything, mock.Anything).Return(nil)

	p := ingest.NewPipeline(reg, eng, out)

	accepting := []ingest.IngestRecord{conforming(), conforming()}
	rejecting := []ingest.IngestRecord{nonconforming(), nonconforming()}

	var wg sync.WaitGroup
	var acceptRes, rejectRes ingest.IngestResult
	var acceptErr, rejectErr error
	wg.Add(2)
	go func() { defer wg.Done(); acceptRes, acceptErr = p.Ingest(context.Background(), accepting) }()
	go func() { defer wg.Done(); rejectRes, rejectErr = p.Ingest(context.Background(), rejecting) }()
	wg.Wait()

	require.NoError(t, acceptErr)
	require.NoError(t, rejectErr)
	assert.Equal(t, ingest.IngestResult{Accepted: 2, Rejected: 0}, acceptRes)
	assert.Equal(t, ingest.IngestResult{Accepted: 0, Rejected: 2}, rejectRes)
}
