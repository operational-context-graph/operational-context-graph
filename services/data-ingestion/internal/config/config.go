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
	"errors"
	"fmt"
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

// envPrefix namespaces environment variables for this service.
// Double-underscore (__) separates hierarchy levels; single underscore is part of a field name.
// For example OCG_DI_GRPC__TLS_CERT_FILE maps to the grpc.tls_cert_file key.
const envPrefix = "OCG_DI_"

// envFileKey is the environment variable that points to an optional config file.
const envFileKey = envPrefix + "CONFIG_FILE"

// Config is the validated runtime configuration.
type Config struct {
	Server  ServerConfig  `koanf:"server"`
	Logging LoggingConfig `koanf:"logging"`
	GRPC    GRPCConfig    `koanf:"grpc"`
	Limits  LimitsConfig  `koanf:"limits"`
	Retry   RetryConfig   `koanf:"retry"`
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

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	Host            string        `koanf:"host" validate:"required"`
	Port            int           `koanf:"port" validate:"required,min=1,max=65535"`
	TLSCertFile     string        `koanf:"tls_cert_file"`
	TLSKeyFile      string        `koanf:"tls_key_file"`
	TLSCAFile       string        `koanf:"tls_ca_file"`
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
}

// LimitsConfig holds request-level limit settings for the gRPC handler.
type LimitsConfig struct {
	MaxMessages    int   `koanf:"max_messages"`
	MaxPayloadBytes int64 `koanf:"max_payload_bytes"`
}

// RetryConfig holds retry hint settings returned in UNAVAILABLE responses.
// The server always returns InitialBackoff as the hint; Multiplier and
// MaxBackoff are validated and stored for future per-client backoff support.
type RetryConfig struct {
	InitialBackoff time.Duration `koanf:"initial_backoff"`
	MaxBackoff     time.Duration `koanf:"max_backoff"`
	Multiplier     float64       `koanf:"multiplier"`
}

// defaults returns the built-in configuration applied before any override.
func defaults() *Config {
	return &Config{
		Server:  ServerConfig{Host: "0.0.0.0", Port: 8080},
		Logging: LoggingConfig{Level: "info"},
		GRPC: GRPCConfig{
			Host:            "0.0.0.0",
			Port:            9090,
			ShutdownTimeout: 30 * time.Second,
		},
		Limits: LimitsConfig{
			MaxMessages:    1000,
			MaxPayloadBytes: 4 * 1024 * 1024, // 4 MiB
		},
		Retry: RetryConfig{
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     60 * time.Second,
			Multiplier:     1.0,
		},
	}
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
	//   OCG_DI_GRPC__ADDR             -> grpc.addr
	//   OCG_DI_GRPC__TLS_CERT_FILE    -> grpc.tls_cert_file
	//   OCG_DI_LIMITS__MAX_MESSAGES   -> limits.max_messages
	//   OCG_DI_RETRY__INITIAL_BACKOFF -> retry.initial_backoff
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

// validateConfig runs structural validation followed by cross-field checks.
func validateConfig(cfg *Config) error {
	if err := validator.New().Struct(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	if err := validateTLS(&cfg.GRPC); err != nil {
		return err
	}
	if err := validateLimits(&cfg.Limits); err != nil {
		return err
	}
	if err := validateRetry(&cfg.Retry); err != nil {
		return err
	}

	return nil
}

func validateTLS(g *GRPCConfig) error {
	set := 0
	for _, p := range []string{g.TLSCertFile, g.TLSKeyFile, g.TLSCAFile} {
		if p != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		return errors.New("validate config: grpc tls requires all three of tls_cert_file, tls_key_file, tls_ca_file or none")
	}
	if g.ShutdownTimeout <= 0 {
		return errors.New("validate config: grpc.shutdown_timeout must be positive")
	}
	return nil
}

func validateLimits(l *LimitsConfig) error {
	if l.MaxMessages <= 0 {
		return errors.New("validate config: limits.max_messages must be positive")
	}
	if l.MaxPayloadBytes <= 0 {
		return errors.New("validate config: limits.max_payload_bytes must be positive")
	}
	return nil
}

func validateRetry(r *RetryConfig) error {
	if r.InitialBackoff <= 0 {
		return errors.New("validate config: retry.initial_backoff must be positive")
	}
	if r.MaxBackoff <= 0 {
		return errors.New("validate config: retry.max_backoff must be positive")
	}
	if r.MaxBackoff < r.InitialBackoff {
		return errors.New("validate config: retry.max_backoff must be >= initial_backoff")
	}
	if r.Multiplier < 1.0 {
		return errors.New("validate config: retry.multiplier must be >= 1.0")
	}
	return nil
}
