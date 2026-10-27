// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"

	"github.com/operational-context-graph/inventory-store/internal/apperr"
)

var (
	ErrAssetNotFound        = fmt.Errorf("asset not found: %w", apperr.ErrNotFound)
	ErrAssetAlreadyExists   = fmt.Errorf("asset already exists: %w", apperr.ErrAlreadyExists)
	ErrRelationshipNotFound = fmt.Errorf("relationship not found: %w", apperr.ErrNotFound)
	ErrInvalidInput         = fmt.Errorf("invalid input: %w", apperr.ErrInvalidInput)
)
