-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- Creates the ocg.traces table.
-- replication_num must be 1 on a single-BE dev cluster; use 3 in production.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS ocg.traces (
  `timestamp`            DATETIME(6)   NOT NULL,
  `iri`                  VARCHAR(512)  NOT NULL,
  `observed_timestamp`   DATETIME(6)   NULL,
  `host_name`            VARCHAR(256)  NULL,
  `trace_id`             VARCHAR(32)   NULL,
  `span_id`              VARCHAR(16)   NULL,
  `parent_span_id`       VARCHAR(16)   NULL,
  `span_name`            VARCHAR(256)  NULL,
  `duration_nanos`       BIGINT        NULL,
  `status_code`          VARCHAR(16)   NULL,
  `resource_attributes`  VARIANT       NULL,
  `attributes`           VARIANT       NULL,

  INDEX idx_trace_id  (`trace_id`)  USING INVERTED,
  INDEX idx_span_id   (`span_id`)   USING INVERTED,
  INDEX idx_host_name (`host_name`) USING INVERTED,
  INDEX idx_span_name (`span_name`) USING INVERTED
  )
  ENGINE = OLAP
  UNIQUE KEY(`timestamp`, `iri`)
  PARTITION BY RANGE(`timestamp`) ()
  DISTRIBUTED BY HASH(`iri`) BUCKETS 10
  PROPERTIES (
               "compression"                       = "zstd",
               "compaction_policy"                 = "time_series",
               "light_schema_change"               = "true",
               "replication_num"                   = "3",
               "dynamic_partition.enable"          = "true",
               "dynamic_partition.time_unit"       = "DAY",
               "dynamic_partition.start"           = "-6",
               "dynamic_partition.end"             = "1",
               "dynamic_partition.prefix"          = "p",
               "dynamic_partition.buckets"         = "10",
               "dynamic_partition.replication_num" = "3"
             );
-- +goose StatementEnd

-- +goose Down
