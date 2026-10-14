// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

// Package migrations embeds the SQL migration files for the inventory-store schema
// and exposes a Goose Provider pre-configured for PostgreSQL.
package migrations

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embeddedFS embed.FS

// NewProvider returns a Goose provider targeting the embedded SQL files.
// The caller must ensure the database exists and is reachable before calling.
func NewProvider(db *sql.DB) (*goose.Provider, error) {
	p, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		embeddedFS,
	)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return p, nil
}
