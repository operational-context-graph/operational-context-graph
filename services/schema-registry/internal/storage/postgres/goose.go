// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import "github.com/pressly/goose/v3"

// SetMigrationDialect configures Goose before module-owned migrations run.
func SetMigrationDialect(dialect string) error {
	return goose.SetDialect(dialect)
}
