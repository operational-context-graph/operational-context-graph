-- +goose Up
-- +goose ENVSUB ON
CREATE TABLE IF NOT EXISTS ${DORIS_DATABASE}.traces (
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
DISTRIBUTED BY HASH(`iri`) BUCKETS ${DORIS_BUCKETS}
PROPERTIES (
  "compression"                       = "zstd",
  "light_schema_change"               = "true",
  "replication_num"                   = "${DORIS_REPLICATION_NUM}",
  "dynamic_partition.enable"          = "true",
  "dynamic_partition.time_unit"       = "DAY",
  "dynamic_partition.start"           = "-6",
  "dynamic_partition.end"             = "1",
  "dynamic_partition.prefix"          = "p",
  "dynamic_partition.buckets"         = "${DORIS_BUCKETS}",
  "dynamic_partition.replication_num" = "${DORIS_REPLICATION_NUM}"
);

-- +goose Down
DROP TABLE IF EXISTS ${DORIS_DATABASE}.traces;
