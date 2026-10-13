// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopIngestorAcceptsAll(t *testing.T) {
	n := &NoopIngestor{}
	batch := []IngestRecord{{Payload: []byte("x")}, {Payload: []byte("y")}}
	result, err := n.Ingest(context.Background(), batch)
	require.NoError(t, err)
	assert.Equal(t, len(batch), result.Accepted)
	assert.Equal(t, 0, result.Rejected)
}

func TestNoopIngestorEmptyBatch(t *testing.T) {
	n := &NoopIngestor{}
	result, err := n.Ingest(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Accepted)
}

func TestErrOverloadedIsDistinct(t *testing.T) {
	assert.True(t, errors.Is(ErrOverloaded, ErrOverloaded))
	assert.False(t, errors.Is(ErrOverloaded, errors.New("other")))
}

func TestErrOverloadedWrapped(t *testing.T) {
	wrapped := errors.Join(ErrOverloaded, errors.New("extra"))
	assert.True(t, errors.Is(wrapped, ErrOverloaded))
}
