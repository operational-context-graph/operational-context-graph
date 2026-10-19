// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package introspection loads Schema Introspection API configuration.
package introspection

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const (
	envPrefix  = "OCG_SCHEMA_INTROSPECTION_"
	envFileKey = envPrefix + "CONFIG_FILE"

	maxPreviousKeys = 3
	// maxCursorKeyLifetime limits the replay window after a key compromise.
	maxCursorKeyLifetime = 24 * time.Hour
)

// Serviceability limits are fixed by the version 1 contract.
const (
	MaxArtifactBytes      int64 = 16 * 1024 * 1024
	MaxGeneratedJSONBytes int64 = 64 * 1024 * 1024
	MaxJSONDepth                = 64
	MaxObjectMembers            = 256
	MaxArrayEntries             = 10000
	MaxJSONStringBytes    int64 = 1024 * 1024
)

// Config is the validated runtime configuration.
type Config struct {
	Server    ServerConfig    `koanf:"server"`
	Database  DatabaseConfig  `koanf:"database"`
	Admission AdmissionConfig `koanf:"admission"`
	Cursor    CursorConfig    `koanf:"cursor"`
	Logging   LoggingConfig   `koanf:"logging"`
}

type ServerConfig struct {
	Host              string        `koanf:"host" validate:"required"`
	Port              int           `koanf:"port" validate:"min=1,max=65535"`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout" validate:"gt=0"`
	ReadTimeout       time.Duration `koanf:"read_timeout" validate:"gt=0"`
	WriteTimeout      time.Duration `koanf:"write_timeout" validate:"gt=0"`
	IdleTimeout       time.Duration `koanf:"idle_timeout" validate:"gt=0"`
	ShutdownTimeout   time.Duration `koanf:"shutdown_timeout" validate:"gt=0"`
}

type DatabaseConfig struct {
	URL                      string        `koanf:"url" validate:"required"`
	Production               bool          `koanf:"production"`
	PoolMinConnections       int32         `koanf:"pool_min_connections" validate:"gte=0"`
	PoolMaxConnections       int32         `koanf:"pool_max_connections" validate:"gte=2"`
	ReservedConnections      int32         `koanf:"reserved_connections" validate:"gte=1"`
	AcquireTimeout           time.Duration `koanf:"acquire_timeout" validate:"gt=0"`
	QueryTimeout             time.Duration `koanf:"query_timeout" validate:"gt=0"`
	StatementTimeout         time.Duration `koanf:"statement_timeout" validate:"gt=0"`
	LockTimeout              time.Duration `koanf:"lock_timeout" validate:"gt=0"`
	IdleInTransactionTimeout time.Duration `koanf:"idle_in_transaction_timeout" validate:"gt=0"`
}

type AdmissionConfig struct {
	MaxInFlight   int           `koanf:"max_in_flight" validate:"gt=0"`
	RetryInterval time.Duration `koanf:"retry_interval" validate:"gte=0"`
}

type CursorConfig struct {
	ActiveKey    CursorKey   `koanf:"active_key"`
	PreviousKeys []CursorKey `koanf:"previous_keys"`
}

type CursorKey struct {
	ID       string    `koanf:"id"`
	Secret   string    `koanf:"secret"`
	Deadline time.Time `koanf:"deadline"`
}

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
		key = strings.TrimPrefix(key, envPrefix)
		return strings.ReplaceAll(strings.ToLower(key), "__", "."), value
	}
	if err := k.Load(env.Provider(".", env.Opt{Prefix: envPrefix, TransformFunc: transform}), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result: cfg, WeaklyTypedInput: true,
			DecodeHook: mapstructure.ComposeDecodeHookFunc(mapstructure.StringToTimeDurationHookFunc()),
		},
	}); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

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

func validate(cfg *Config) error {
	if err := validator.New().Struct(cfg); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	if err := validateDatabase(cfg.Database); err != nil {
		return err
	}
	if cfg.Admission.MaxInFlight > int(cfg.Database.PoolMaxConnections-cfg.Database.ReservedConnections) {
		return fmt.Errorf("validate configuration: admission.max_in_flight exceeds unreserved database capacity")
	}
	if err := validateCursor(cfg.Cursor); err != nil {
		return err
	}
	return nil
}

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
	if parsed.Query().Get("sslmode") != "verify-full" {
		return fmt.Errorf("validate configuration: database connections require sslmode=verify-full")
	}
	return nil
}

func validateCursor(cfg CursorConfig) error {
	now := time.Now()
	if err := validateCursorKey(cfg.ActiveKey, "active", now); err != nil {
		return err
	}
	if len(cfg.PreviousKeys) > maxPreviousKeys {
		return fmt.Errorf("validate configuration: too many previous cursor keys")
	}
	seen := map[string]struct{}{cfg.ActiveKey.ID: {}}
	for _, key := range cfg.PreviousKeys {
		if err := validateCursorKey(key, "previous", now); err != nil {
			return err
		}
		if key.Deadline.After(cfg.ActiveKey.Deadline) {
			return fmt.Errorf("validate configuration: previous cursor key deadline exceeds active key deadline")
		}
		if _, exists := seen[key.ID]; exists {
			return fmt.Errorf("validate configuration: duplicate cursor key identifier")
		}
		seen[key.ID] = struct{}{}
	}
	return nil
}

func validateCursorKey(key CursorKey, kind string, now time.Time) error {
	if key.ID == "" || key.Deadline.IsZero() || !key.Deadline.After(now) {
		return fmt.Errorf("validate configuration: %s cursor key must have an identifier and future deadline", kind)
	}
	if key.Deadline.After(now.Add(maxCursorKeyLifetime)) {
		return fmt.Errorf("validate configuration: %s cursor key deadline exceeds %s", kind, maxCursorKeyLifetime)
	}
	decoded, err := base64.StdEncoding.DecodeString(key.Secret)
	if err != nil || len(decoded) < 32 {
		return fmt.Errorf("validate configuration: %s cursor key must be base64 encoded and at least 256 bits", kind)
	}
	return nil
}

// Redacted returns a log-safe summary. It never exposes database credentials or keys.
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"server_host": c.Server.Host, "server_port": c.Server.Port,
		"database_host": databaseHost(c.Database.URL), "pool_max_connections": c.Database.PoolMaxConnections,
		"reserved_connections": c.Database.ReservedConnections, "max_in_flight": c.Admission.MaxInFlight,
		"cursor_active_key_id": c.Cursor.ActiveKey.ID,
	}
}

func databaseHost(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return parsed.Host
}
