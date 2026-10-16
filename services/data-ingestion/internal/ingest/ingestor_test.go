// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrOverloadedIsDistinct(t *testing.T) {
	assert.True(t, errors.Is(ErrOverloaded, ErrOverloaded))
	assert.False(t, errors.Is(ErrOverloaded, errors.New("other")))
}

func TestErrOverloadedWrapped(t *testing.T) {
	wrapped := errors.Join(ErrOverloaded, errors.New("extra"))
	assert.True(t, errors.Is(wrapped, ErrOverloaded))
}

func TestErrUnavailableIsDistinct(t *testing.T) {
	assert.True(t, errors.Is(ErrUnavailable, ErrUnavailable))
	assert.False(t, errors.Is(ErrUnavailable, errors.New("other")))
}

func TestErrUnavailableWrapped(t *testing.T) {
	cause := errors.New("connection refused")
	wrapped := fmt.Errorf("%w: schema registry down: %w", ErrUnavailable, cause)
	assert.True(t, errors.Is(wrapped, ErrUnavailable))
	assert.True(t, errors.Is(wrapped, cause))
}
