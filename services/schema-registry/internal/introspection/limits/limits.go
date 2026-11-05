// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package limits defines fixed Schema Introspection API serviceability limits.
package limits

// MaxGeneratedJSONBytes bounds each generated identity JSON representation.
const MaxGeneratedJSONBytes int64 = 64 * 1024 * 1024
