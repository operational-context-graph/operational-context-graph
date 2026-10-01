// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package store

import "errors"

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")
