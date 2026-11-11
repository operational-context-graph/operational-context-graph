package postgres

import "github.com/pressly/goose/v3"

// SetMigrationDialect configures Goose before module-owned migrations run.
func SetMigrationDialect(dialect string) error {
	return goose.SetDialect(dialect)
}
