-- +goose Up
-- Rename the default goose tracking table to the service-specific name
-- configured via GOOSE_TABLE=goose_db_version_data_ingestion in the Helm chart.
-- IF EXISTS makes this safe on fresh deployments where the default table was
-- never created.
ALTER TABLE IF EXISTS goose_db_version
    RENAME TO goose_db_version_data_ingestion;

-- +goose Down
ALTER TABLE IF EXISTS goose_db_version_data_ingestion
    RENAME TO goose_db_version;
