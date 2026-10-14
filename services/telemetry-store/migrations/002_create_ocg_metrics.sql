-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS ocg.metrics (
  `timestamp`            DATETIME(6)   NOT NULL,
  `iri`                  VARCHAR(512)  NOT NULL,
  `observed_timestamp`   DATETIME(6)   NULL,
  `host_name`            VARCHAR(256)  NULL,
  `metric_name`          VARCHAR(256)  NULL,
  `value`                DOUBLE        NULL,
  `resource_attributes`  VARIANT       NULL,
  `attributes`           VARIANT       NULL,

  INDEX idx_host_name   (`host_name`)   USING INVERTED,
  INDEX idx_metric_name (`metric_name`) USING INVERTED
)
ENGINE = OLAP
UNIQUE KEY(`timestamp`, `iri`)
PARTITION BY RANGE(`timestamp`) ()
DISTRIBUTED BY HASH(`iri`) BUCKETS ${BUCKETS:-10}
PROPERTIES (
  "compression"                       = "zstd",
  "compaction_policy"                 = "time_series",
  "light_schema_change"               = "true",
  "replication_num"                   = "${REPLICATION_NUM:-3}",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "${BUCKETS:-10}",
  "dynamic_partition.replication_num" = "${REPLICATION_NUM:-3}"
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ocg.metrics;
-- +goose StatementEnd
