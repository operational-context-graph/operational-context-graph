// Package dorismigrate provides a Goose dialect for Apache Doris.
// It implements dialect.Querier so Goose's Provider API can manage a version
// tracking table using DDL that Doris accepts.
package dorismigrate

import (
	"fmt"
	"os"
)

// Querier implements dialect.Querier for Apache Doris.
//
// Doris rejects standard MySQL tracking table DDL because it requires a key
// model declaration and a distribution clause. This implementation produces
// DDL that Doris accepts, using a UNIQUE KEY Merge-on-Write table keyed on
// version_id.
//
// The replication_num is read from the DORIS_REPLICATION_NUM environment
// variable at call time (defaults to "1" if unset).
type Querier struct{}

// CreateTable returns DDL to create the Goose version tracking table in Doris.
func (q *Querier) CreateTable(tableName string) string {
	replicationNum := os.Getenv("DORIS_REPLICATION_NUM")
	if replicationNum == "" {
		replicationNum = "1"
	}
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    version_id   BIGINT   NOT NULL,
    is_applied   BOOLEAN  NOT NULL,
    tstamp       DATETIME NULL DEFAULT CURRENT_TIMESTAMP
)
UNIQUE KEY(version_id)
DISTRIBUTED BY HASH(version_id) BUCKETS 1
PROPERTIES (
    "replication_num" = "%s"
)`, tableName, replicationNum)
}

// InsertVersion returns SQL to insert a migration record.
// Goose passes (version_id, is_applied) as arguments.
func (q *Querier) InsertVersion(tableName string) string {
	return fmt.Sprintf(
		`INSERT INTO %s (version_id, is_applied) VALUES (?, ?)`,
		tableName,
	)
}

// DeleteVersion returns SQL to delete a migration record.
// Goose passes (version_id) as the argument.
func (q *Querier) DeleteVersion(tableName string) string {
	return fmt.Sprintf(`DELETE FROM %s WHERE version_id = ?`, tableName)
}

// GetMigrationByVersion returns SQL to fetch a single migration by version.
// Goose scans (tstamp, is_applied) from the result.
func (q *Querier) GetMigrationByVersion(tableName string) string {
	return fmt.Sprintf(
		`SELECT tstamp, is_applied FROM %s WHERE version_id = ? ORDER BY tstamp DESC LIMIT 1`,
		tableName,
	)
}

// ListMigrations returns SQL to list all migration records, newest first.
// Goose scans (version_id, is_applied) from each row.
func (q *Querier) ListMigrations(tableName string) string {
	return fmt.Sprintf(
		`SELECT version_id, is_applied FROM %s ORDER BY version_id DESC`,
		tableName,
	)
}

// GetLatestVersion returns SQL to get the highest applied version id.
// Goose scans a nullable int64 from the result.
func (q *Querier) GetLatestVersion(tableName string) string {
	return fmt.Sprintf(
		`SELECT MAX(version_id) FROM %s`,
		tableName,
	)
}
