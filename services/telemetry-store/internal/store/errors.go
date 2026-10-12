// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"

	"github.com/operational-context-graph/telemetry-store/internal/apperr"
)

var (
	ErrLogNotFound    = fmt.Errorf("log not found: %w", apperr.ErrNotFound)
	ErrMetricNotFound = fmt.Errorf("metric not found: %w", apperr.ErrNotFound)
	ErrTraceNotFound  = fmt.Errorf("trace not found: %w", apperr.ErrNotFound)
)
