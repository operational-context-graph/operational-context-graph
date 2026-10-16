// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Package migrations embeds the Goose SQL migration files for the inventory-store schema.
package migrations

import "embed"

// FS contains all *.sql migration files in this directory.
//
//go:embed *.sql
var FS embed.FS
