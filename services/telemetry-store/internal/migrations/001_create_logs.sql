-- +goose Up
-- +goose ENVSUB ON
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
DROP TABLE IF EXISTS ocg.logs;
