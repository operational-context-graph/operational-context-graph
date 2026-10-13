-- SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
-- SPDX-License-Identifier: Apache-2.0
--
-- Creates the ocg.logs table.
-- replication_num must be 1 on a single-BE dev cluster; use 3 in production (SDD §4.6).
-- See: SDD §4.3 and Appendix A.

CREATE DATABASE IF NOT EXISTS ocg;

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
