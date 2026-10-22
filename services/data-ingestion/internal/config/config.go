// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package config loads and validates the Data Ingestion service configuration.
//
// Configuration is layered: built-in defaults, then an optional YAML file,
// then environment variables prefixed with OCG_DI_. The resulting struct is
// validated with go-playground/validator before it is returned.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// envPrefix namespaces environment variables for this service.
// Double-underscore (__) separates hierarchy levels; single underscore is part of a field name.
// For example OCG_DI_GRPC__TLS__CERT_FILE maps to the grpc.tls.cert_file key.
const envPrefix = "OCG_DI_"

// envFileKey is the environment variable that points to an optional config file.
const envFileKey = envPrefix + "CONFIG_FILE"

// Config is the validated runtime configuration.
type Config struct {
	Server         ServerConfig         `koanf:"server"`
	Logging        LoggingConfig        `koanf:"logging"`
	Database       DatabaseConfig       `koanf:"database"`
	GRPC           GRPCConfig           `koanf:"grpc"`
	Limits         LimitsConfig         `koanf:"limits"`
	Retry          RetryConfig          `koanf:"retry"`
	Drain          DrainConfig          `koanf:"drain"`
	TelemetryStore TelemetryStoreConfig `koanf:"telemetry_store"`
	InventoryStore InventoryStoreConfig `koanf:"inventory_store"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host string `koanf:"host" validate:"required"`
	Port int    `koanf:"port" validate:"required,min=1,max=65535"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level string `koanf:"level" validate:"required,oneof=debug info warn error"`
}

// DatabaseConfig holds PostgreSQL connection settings for the outbox. The
// password is supplied only via OCG_DI_DATABASE__PASSWORD (from a Secret); it is
// never a committed config-file field.
type DatabaseConfig struct {
	Host     string `koanf:"host" validate:"required"`
	Port     int    `koanf:"port" validate:"required,min=1,max=65535"`
	Name     string `koanf:"name" validate:"required"`
	User     string `koanf:"user" validate:"required"`
	Password string `koanf:"password" validate:"required"`
	SSLMode  string `koanf:"sslmode" validate:"required,oneof=disable allow prefer require verify-ca verify-full"`
	MaxConns int    `koanf:"maxconns" validate:"min=0"`
}

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	Host            string        `koanf:"host"             validate:"required"`
	Port            int           `koanf:"port"             validate:"required,min=1,max=65535"`
	TLS             *TLSConfig    `koanf:"tls"              validate:"omitempty"`
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout" validate:"required,gt=0"`
}

// TLSConfig holds mTLS certificate paths. All three fields are required together;
// omit the tls block entirely to disable TLS.
type TLSConfig struct {
	CertFile string `koanf:"cert_file" validate:"required"`
	KeyFile  string `koanf:"key_file"  validate:"required"`
	CAFile   string `koanf:"ca_file"   validate:"required"`
}

// LimitsConfig holds request-level limit settings for the gRPC handler.
type LimitsConfig struct {
	MaxMessages     int   `koanf:"max_messages"    validate:"required,gt=0"`
	MaxPayloadBytes int64 `koanf:"max_payload_bytes" validate:"required,gt=0"`
}

// RetryConfig holds retry hint settings returned in UNAVAILABLE responses.
// The server always returns InitialBackoff as the hint; Multiplier and
// MaxBackoff are validated and stored for future per-client backoff support.
type RetryConfig struct {
	InitialBackoff time.Duration `koanf:"initial_backoff" validate:"required,gt=0"`
	MaxBackoff     time.Duration `koanf:"max_backoff"     validate:"required,gtefield=InitialBackoff"`
	Multiplier     float64       `koanf:"multiplier"      validate:"required,gte=1.0"`
}

// DrainConfig holds the outbox drain's runtime settings.
// PollInterval bounds how often a worker re-scans the outbox when idle.
// The backoff fields bound per-store retry on store overload.
// MaxRetries bounds the retries for one batch; 0 means retry forever.
// PageSize bounds how many pending rows a worker scans per pass.
type DrainConfig struct {
	PollInterval   time.Duration `koanf:"poll_interval"    validate:"required,gt=0"`
	BackoffInitial time.Duration `koanf:"backoff_initial"  validate:"required,gt=0"`
	BackoffMax     time.Duration `koanf:"backoff_max"      validate:"required,gtefield=BackoffInitial"`
	BackoffJitter  float64       `koanf:"backoff_jitter"   validate:"gte=0,lte=1"`
	MaxRetries     int           `koanf:"max_retries"      validate:"gte=0"`
	PageSize       int           `koanf:"page_size"        validate:"min=1"`
}

// TelemetryStoreConfig holds the connection settings for the Telemetry Store
// ingestion API used by the telemetry delivery adapter.
// Base URL is supplied via OCG_DI_TELEMETRY_STORE__BASE_URL.
type TelemetryStoreConfig struct {
	BaseURL    string        `koanf:"base_url"    validate:"required,url"`
	Timeout    time.Duration `koanf:"timeout"`
	MaxRecords int           `koanf:"max_records" validate:"gte=0"`
	MaxBytes   int64         `koanf:"max_bytes"   validate:"gte=0"`
	MaxLinger  time.Duration `koanf:"max_linger"`
}

// InventoryStoreConfig holds the connection settings for the Inventory Store
// REST API used by the inventory delivery adapter.
// Base URL is supplied via OCG_DI_INVENTORY_STORE__BASE_URL.
type InventoryStoreConfig struct {
	BaseURL    string        `koanf:"base_url"    validate:"required,url"`
	Timeout    time.Duration `koanf:"timeout"`
	MaxRecords int           `koanf:"max_records" validate:"gte=0"`
	MaxBytes   int64         `koanf:"max_bytes"   validate:"gte=0"`
	MaxLinger  time.Duration `koanf:"max_linger"`
}

// defaults returns the built-in configuration applied before any override.
func defaults() *Config {
	return &Config{
		Server:  ServerConfig{Host: "0.0.0.0", Port: 8080},
		Logging: LoggingConfig{Level: "info"},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			Name:     "ocg",
			User:     "ocg",
			SSLMode:  "disable",
			MaxConns: 10,
		},
		GRPC: GRPCConfig{
			Host:            "0.0.0.0",
			Port:            9090,
			ShutdownTimeout: 30 * time.Second,
		},
		Limits: LimitsConfig{
			MaxMessages:     1000,
			MaxPayloadBytes: 4 * 1024 * 1024, // 4 MiB
		},
		Retry: RetryConfig{
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     60 * time.Second,
			Multiplier:     1.0,
		},
		Drain: DrainConfig{
			PollInterval:   1 * time.Second,
			BackoffInitial: 1 * time.Second,
			BackoffMax:     60 * time.Second,
			BackoffJitter:  0.2,
			MaxRetries:     0,   // unbounded by default
			PageSize:       500, // rows per scan pass
		},
		TelemetryStore: TelemetryStoreConfig{
			BaseURL: "http://telemetry-store:8080",
			Timeout: 120 * time.Second,
		},
		InventoryStore: InventoryStoreConfig{
			BaseURL: "http://inventory-store:8080",
			Timeout: 30 * time.Second,
		},
	}
}

// DatabaseDSN builds the pgx connection string from the Database settings. The
// password is URL-escaped; pool_max_conns is set when MaxConns is positive.
func (c *Config) DatabaseDSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Database.User, c.Database.Password),
		Host:   net.JoinHostPort(c.Database.Host, strconv.Itoa(c.Database.Port)),
		Path:   "/" + c.Database.Name,
	}
	q := url.Values{}
	q.Set("sslmode", c.Database.SSLMode)
	if c.Database.MaxConns > 0 {
		q.Set("pool_max_conns", strconv.Itoa(c.Database.MaxConns))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Load builds the configuration from defaults, an optional file referenced by
// OCG_DI_CONFIG_FILE, and OCG_DI_* environment variables.
func Load() (*Config, error) {
	return LoadFromFile(os.Getenv(envFileKey))
}

// LoadFromFile builds the configuration, reading the given YAML file when path
// is non-empty. An empty path skips file loading and relies on defaults and
// environment variables.
func LoadFromFile(path string) (*Config, error) {
	cfg := defaults()
	k := koanf.New(".")

	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("load config file %q: %w", path, err)
		}
	}

	// Map env vars to koanf keys.
	// Double underscore (__) separates hierarchy levels; single underscore is preserved as part of a field name.
	// Examples:
	//   OCG_DI_GRPC__ADDR               -> grpc.addr
	//   OCG_DI_GRPC__TLS__CERT_FILE     -> grpc.tls.cert_file
	//   OCG_DI_LIMITS__MAX_MESSAGES     -> limits.max_messages
	//   OCG_DI_RETRY__INITIAL_BACKOFF   -> retry.initial_backoff
	transform := func(k, v string) (string, any) {
		k = strings.TrimPrefix(k, envPrefix)
		k = strings.ToLower(k)
		return strings.ReplaceAll(k, "__", "."), v
	}
	if err := k.Load(env.Provider(".", env.Opt{Prefix: envPrefix, TransformFunc: transform}), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}

	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           cfg,
			WeaklyTypedInput: true,
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
			),
		},
	}); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validateConfig validates the configuration using struct tags.
func validateConfig(cfg *Config) error {
	if err := validator.New().Struct(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}
