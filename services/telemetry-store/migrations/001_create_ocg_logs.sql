-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- +goose Up
-- +goose StatementBegin
CREATE DATABASE IF NOT EXISTS ocg;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS ocg.logs (
  `timestamp`            DATETIME(6)   NOT NULL,
  `iri`                  VARCHAR(512)  NOT NULL,
  `observed_timestamp`   DATETIME(6)   NULL,
  `host_name`            VARCHAR(256)  NULL,
  `severity`             VARCHAR(16)   NULL,
  `body`                 TEXT          NULL,
  `resource_attributes`  VARIANT       NULL,
  `attributes`           VARIANT       NULL,

  INDEX idx_host_name (`host_name`) USING INVERTED,
  INDEX idx_severity  (`severity`)  USING INVERTED,
  INDEX idx_body      (`body`)      USING INVERTED
  PROPERTIES("parser" = "unicode", "support_phrase" = "true")
)
ENGINE = OLAP
UNIQUE KEY(`timestamp`, `iri`)
PARTITION BY RANGE(`timestamp`) ()
DISTRIBUTED BY HASH(`iri`) BUCKETS ${BUCKETS}
PROPERTIES (
  "compression"                       = "zstd",
  "light_schema_change"               = "true",
  "replication_num"                   = "${REPLICATION_NUM}",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "${BUCKETS}",
  "dynamic_partition.replication_num" = "${REPLICATION_NUM}"
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ocg.logs;
-- +goose StatementEnd
