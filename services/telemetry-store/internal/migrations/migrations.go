// Package migrations embeds the Goose SQL migration files for the Telemetry Store.
package migrations

import "embed"

// FS holds all *.sql migration files for embed access by the migration runner.
//
//go:embed *.sql
var FS embed.FS
