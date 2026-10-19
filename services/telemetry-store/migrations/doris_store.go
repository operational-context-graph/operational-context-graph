// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3/database"
)

const dorisVersionTable = "ocg.goose_db_version"

// dorisStore implements database.Store with Doris-compatible DDL and queries.
//
// Doris does not support BIGINT UNSIGNED (rejected by mysql dialect) or the StarRocks PRIMARY KEY
// table syntax. The UNIQUE KEY table model avoids both issues. Because there is no AUTO_INCREMENT
// on UNIQUE KEY tables, we use version_id as the id value — migration versions are unique integers.
type dorisStore struct{}

var _ database.Store = (*dorisStore)(nil)

func (s *dorisStore) Tablename() string {
	return dorisVersionTable
}

func (s *dorisStore) CreateVersionTable(ctx context.Context, db database.DBTxConn) error {
	q := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id         bigint   NOT NULL,
		version_id bigint   NOT NULL,
		is_applied boolean  NOT NULL,
		tstamp     datetime NULL DEFAULT CURRENT_TIMESTAMP
	) UNIQUE KEY (id)
	DISTRIBUTED BY HASH (id) BUCKETS 1
	PROPERTIES ("replication_num" = "1")`, dorisVersionTable)
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("create version table: %w", err)
	}
	return nil
}

func (s *dorisStore) Insert(ctx context.Context, db database.DBTxConn, req database.InsertRequest) error {
	q := fmt.Sprintf(`INSERT INTO %s (id, version_id, is_applied) VALUES (?, ?, ?)`, dorisVersionTable)
	// Use version_id as the unique row id; migration versions are unique non-negative integers.
	if _, err := db.ExecContext(ctx, q, req.Version, req.Version, true); err != nil {
		return fmt.Errorf("insert version %d: %w", req.Version, err)
	}
	return nil
}

func (s *dorisStore) Delete(ctx context.Context, db database.DBTxConn, version int64) error {
	q := fmt.Sprintf(`DELETE FROM %s WHERE version_id = ?`, dorisVersionTable)
	if _, err := db.ExecContext(ctx, q, version); err != nil {
		return fmt.Errorf("delete version %d: %w", version, err)
	}
	return nil
}

func (s *dorisStore) GetMigration(ctx context.Context, db database.DBTxConn, version int64) (*database.GetMigrationResult, error) {
	q := fmt.Sprintf(`SELECT tstamp, is_applied FROM %s WHERE version_id = ? ORDER BY tstamp DESC LIMIT 1`, dorisVersionTable)
	var result database.GetMigrationResult
	err := db.QueryRowContext(ctx, q, version).Scan(&result.Timestamp, &result.IsApplied)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %d", database.ErrVersionNotFound, version)
		}
		return nil, fmt.Errorf("get migration %d: %w", version, err)
	}
	return &result, nil
}

func (s *dorisStore) GetLatestVersion(ctx context.Context, db database.DBTxConn) (int64, error) {
	q := fmt.Sprintf(`SELECT MAX(version_id) FROM %s`, dorisVersionTable)
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, q).Scan(&version); err != nil {
		return -1, fmt.Errorf("get latest version: %w", err)
	}
	if !version.Valid {
		return -1, fmt.Errorf("latest %w", database.ErrVersionNotFound)
	}
	return version.Int64, nil
}

func (s *dorisStore) ListMigrations(ctx context.Context, db database.DBTxConn) ([]*database.ListMigrationsResult, error) {
	q := fmt.Sprintf(`SELECT version_id, is_applied FROM %s ORDER BY id DESC`, dorisVersionTable)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()

	var migrations []*database.ListMigrationsResult
	for rows.Next() {
		var r database.ListMigrationsResult
		if err := rows.Scan(&r.Version, &r.IsApplied); err != nil {
			return nil, fmt.Errorf("scan migration: %w", err)
		}
		migrations = append(migrations, &r)
	}
	return migrations, rows.Err()
}
