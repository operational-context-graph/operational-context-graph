// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "errors"

// ErrOverloaded is returned by an Ingestor when the pipeline cannot accept
// more work. Callers MUST use errors.Is to detect it; never match on message text.
var ErrOverloaded = errors.New("pipeline overloaded")
