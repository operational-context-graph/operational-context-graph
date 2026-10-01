-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0

-- Doris DDL for OCG Telemetry Store — local dev
-- replication_num = 1: single-BE dev cluster only.
-- For production set replication_num = 3 and dynamic_partition.replication_num = 3.

CREATE DATABASE IF NOT EXISTS ocg;

CREATE TABLE IF NOT EXISTS ocg.logs (
  `timestamp`   DATETIME(6)   NOT NULL,
  `record_id`   VARCHAR(36)   NOT NULL,
  `host`        VARCHAR(256)  NULL,
  `severity`    VARCHAR(16)   NULL,
  `body`        TEXT          NULL,
  `attributes`  VARIANT       NULL,

  INDEX idx_host     (`host`)     USING INVERTED,
  INDEX idx_severity (`severity`) USING INVERTED,
  INDEX idx_body     (`body`)     USING INVERTED
    PROPERTIES("parser" = "unicode", "support_phrase" = "true")
)
ENGINE = OLAP
DUPLICATE KEY(`timestamp`)
PARTITION BY RANGE(`timestamp`) ()
DISTRIBUTED BY RANDOM BUCKETS 1
PROPERTIES (
  "compression"                       = "zstd",
  "compaction_policy"                 = "time_series",
  "light_schema_change"               = "true",
  "replication_num"                   = "1",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "1",
  "dynamic_partition.replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS ocg.metrics (
  `timestamp`    DATETIME(6)   NOT NULL,
  `record_id`    VARCHAR(36)   NOT NULL,
  `host`         VARCHAR(256)  NULL,
  `metric_name`  VARCHAR(256)  NULL,
  `value`        DOUBLE        NULL,
  `attributes`   VARIANT       NULL,

  INDEX idx_host        (`host`)        USING INVERTED,
  INDEX idx_metric_name (`metric_name`) USING INVERTED
)
ENGINE = OLAP
DUPLICATE KEY(`timestamp`)
PARTITION BY RANGE(`timestamp`) ()
DISTRIBUTED BY RANDOM BUCKETS 1
PROPERTIES (
  "compression"                       = "zstd",
  "compaction_policy"                 = "time_series",
  "light_schema_change"               = "true",
  "replication_num"                   = "1",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "1",
  "dynamic_partition.replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS ocg.traces (
  `timestamp`       DATETIME(6)   NOT NULL,
  `record_id`       VARCHAR(36)   NOT NULL,
  `host`            VARCHAR(256)  NULL,
  `trace_id`        VARCHAR(32)   NULL,
  `span_id`         VARCHAR(16)   NULL,
  `parent_span_id`  VARCHAR(16)   NULL,
  `span_name`       VARCHAR(256)  NULL,
  `duration_nanos`  BIGINT        NULL,
  `status_code`     VARCHAR(16)   NULL,
  `attributes`      VARIANT       NULL,

  INDEX idx_trace_id  (`trace_id`)  USING INVERTED,
  INDEX idx_span_id   (`span_id`)   USING INVERTED,
  INDEX idx_host      (`host`)      USING INVERTED,
  INDEX idx_span_name (`span_name`) USING INVERTED
)
ENGINE = OLAP
DUPLICATE KEY(`timestamp`)
PARTITION BY RANGE(`timestamp`) ()
DISTRIBUTED BY RANDOM BUCKETS 1
PROPERTIES (
  "compression"                       = "zstd",
  "compaction_policy"                 = "time_series",
  "light_schema_change"               = "true",
  "replication_num"                   = "1",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "1",
  "dynamic_partition.replication_num" = "1"
);
