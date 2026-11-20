package config

import (
	"fmt"
	"regexp"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

var identifierRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func validateIdentifier(fl validator.FieldLevel) bool {
	return identifierRe.MatchString(fl.Field().String())
}

// Config holds all runtime configuration for telemetry-store.
type Config struct {
	HTTPPort                string `koanf:"HTTP_PORT"                  validate:"required"`
	DorisDSN                string `koanf:"DORIS_DSN"                  validate:"required"`
	DorisDatabase           string `koanf:"DORIS_DATABASE"             validate:"required"`
	DorisStreamLoadURL      string `koanf:"DORIS_STREAM_LOAD_URL"      validate:"required,url"`
	DorisStreamLoadUser     string `koanf:"DORIS_STREAM_LOAD_USER"     validate:"required"`
	DorisStreamLoadPassword string `koanf:"DORIS_STREAM_LOAD_PASSWORD" validate:"required"`
	LogLevel                string `koanf:"LOG_LEVEL"`
	MaxIngestBodyBytes      int64  `koanf:"MAX_INGEST_BODY_BYTES"`
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	k := koanf.New(".")
	if err := k.Load(env.Provider(".", env.Opt{}), nil); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}
	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.MaxIngestBodyBytes <= 0 {
		cfg.MaxIngestBodyBytes = 10 * 1024 * 1024 * 1024
	}
	if err := validator.New().Struct(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// MigrateConfig holds the minimal configuration needed by the migrate binary.
// It is intentionally separate from Config so the init container does not
// require stream-load or HTTP credentials.
type MigrateConfig struct {
	DorisMigrateDSN string `koanf:"DORIS_MIGRATE_DSN" validate:"required"`
	DorisDatabase   string `koanf:"DORIS_DATABASE"    validate:"required,identifier"`
}

// LoadMigrate reads migrate configuration from environment variables and validates it.
func LoadMigrate() (*MigrateConfig, error) {
	k := koanf.New(".")
	if err := k.Load(env.Provider(".", env.Opt{}), nil); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}
	var cfg MigrateConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	v := validator.New()
	if err := v.RegisterValidation("identifier", validateIdentifier); err != nil {
		return nil, fmt.Errorf("register validator: %w", err)
	}
	if err := v.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}
