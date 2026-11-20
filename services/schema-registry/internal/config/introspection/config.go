// Package introspection loads Schema Introspection API configuration.
package introspection

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const (
	envPrefix  = "OCG_SCHEMA_INTROSPECTION_"
	envFileKey = envPrefix + "CONFIG_FILE"
)

// Config is the validated runtime configuration.
type Config struct {
	Server    ServerConfig    `koanf:"server"`
	Database  DatabaseConfig  `koanf:"database"`
	Admission AdmissionConfig `koanf:"admission"`
	Logging   LoggingConfig   `koanf:"logging"`
}

// ServerConfig bounds listener behavior and request and shutdown lifetimes.
type ServerConfig struct {
	Host              string        `koanf:"host" validate:"required"`
	Port              int           `koanf:"port" validate:"min=1,max=65535"`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout" validate:"gt=0"`
	ReadTimeout       time.Duration `koanf:"read_timeout" validate:"gt=0"`
	WriteTimeout      time.Duration `koanf:"write_timeout" validate:"gt=0"`
	IdleTimeout       time.Duration `koanf:"idle_timeout" validate:"gt=0"`
	ShutdownTimeout   time.Duration `koanf:"shutdown_timeout" validate:"gt=0"`
}

// DatabaseConfig configures user-selected PostgreSQL transport and bounded read transactions.
type DatabaseConfig struct {
	URL                string `koanf:"url" validate:"required"`
	Production         bool   `koanf:"production"`
	PoolMinConnections int32  `koanf:"pool_min_connections" validate:"gte=0"`
	PoolMaxConnections int32  `koanf:"pool_max_connections" validate:"gte=2"`
	// ReservedConnections preserves pool capacity for readiness probes when API admission reaches its limit.
	ReservedConnections      int32         `koanf:"reserved_connections" validate:"gte=1"`
	AcquireTimeout           time.Duration `koanf:"acquire_timeout" validate:"gt=0"`
	QueryTimeout             time.Duration `koanf:"query_timeout" validate:"gt=0"`
	StatementTimeout         time.Duration `koanf:"statement_timeout" validate:"gt=0"`
	LockTimeout              time.Duration `koanf:"lock_timeout" validate:"gt=0"`
	IdleInTransactionTimeout time.Duration `koanf:"idle_in_transaction_timeout" validate:"gt=0"`
}

// AdmissionConfig limits concurrent API requests and supplies retry advice for transient failures.
type AdmissionConfig struct {
	MaxInFlight   int           `koanf:"max_in_flight" validate:"gt=0"`
	RetryInterval time.Duration `koanf:"retry_interval" validate:"gte=0"`
}

// LoggingConfig controls severity filtering and the maximum retained string field length.
type LoggingConfig struct {
	Level          string `koanf:"level" validate:"oneof=debug info warn error"`
	MaxFieldLength int    `koanf:"max_field_length" validate:"gt=0"`
}

// Load loads defaults, then the optional YAML file, then environment overrides.
func Load() (*Config, error) {
	return LoadFromFile(os.Getenv(envFileKey))
}

// LoadFromFile loads defaults, an optional YAML file, and environment overrides.
func LoadFromFile(path string) (*Config, error) {
	cfg := defaults()
	k := koanf.New(".")
	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("load configuration file: %w", err)
		}
	}
	transform := func(key, value string) (string, any) {
		// Double underscores separate nested keys; single underscores remain part of field names.
		key = strings.TrimPrefix(key, envPrefix)
		return strings.ReplaceAll(strings.ToLower(key), "__", "."), value
	}
	if err := k.Load(env.Provider(".", env.Opt{Prefix: envPrefix, TransformFunc: transform}), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}
	if err := k.Unmarshal("", cfg); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaults returns baseline settings; callers MUST supply a database URL and an admission limit.
func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0", Port: 8081, ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second,
			IdleTimeout: 60 * time.Second, ShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			PoolMaxConnections: 8, ReservedConnections: 1, AcquireTimeout: 5 * time.Second,
			QueryTimeout: 5 * time.Second, StatementTimeout: 5 * time.Second,
			LockTimeout: time.Second, IdleInTransactionTimeout: 5 * time.Second,
		},
		Admission: AdmissionConfig{RetryInterval: time.Second},
		Logging:   LoggingConfig{Level: "info", MaxFieldLength: 512},
	}
}

// validate checks field constraints and reserves database capacity outside API admission.
func validate(cfg *Config) error {
	if err := validator.New().Struct(cfg); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	if err := validateDatabase(cfg.Database); err != nil {
		return err
	}
	if cfg.Admission.MaxInFlight > int(cfg.Database.PoolMaxConnections-cfg.Database.ReservedConnections) {
		// Reject oversubscription instead of allowing API traffic to starve readiness probes.
		return fmt.Errorf("validate configuration: admission.max_in_flight exceeds unreserved database capacity")
	}
	return nil
}

// validateDatabase requires an absolute PostgreSQL URL and usable pool capacity, without assessing SSL policy.
func validateDatabase(cfg DatabaseConfig) error {
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" {
		return fmt.Errorf("validate configuration: database.url must be an absolute PostgreSQL URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("validate configuration: database.url must use postgres or postgresql")
	}
	if cfg.PoolMinConnections > cfg.PoolMaxConnections {
		return fmt.Errorf("validate configuration: database pool minimum exceeds maximum")
	}
	if cfg.ReservedConnections >= cfg.PoolMaxConnections {
		return fmt.Errorf("validate configuration: database reserved connections exhaust the pool")
	}
	return nil
}

// Redacted returns a log-safe summary. It never exposes database credentials.
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"server_host": c.Server.Host, "server_port": c.Server.Port,
		"database_host": databaseHost(c.Database.URL), "pool_max_connections": c.Database.PoolMaxConnections,
		"reserved_connections": c.Database.ReservedConnections, "max_in_flight": c.Admission.MaxInFlight,
	}
}

// databaseHost extracts the hostname without retaining user information, ports, or query parameters.
func databaseHost(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return parsed.Host
}
