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
	"os"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// envPrefix namespaces environment variables for this service.
// For example OCG_DI_SERVER_PORT maps to the server.port key.
const envPrefix = "OCG_DI_"

// envFileKey is the environment variable that points to an optional config file.
const envFileKey = envPrefix + "CONFIG_FILE"

// Config is the validated runtime configuration.
type Config struct {
	Server  ServerConfig  `koanf:"server"`
	Logging LoggingConfig `koanf:"logging"`
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

// defaults returns the built-in configuration applied before any override.
func defaults() *Config {
	return &Config{
		Server:  ServerConfig{Host: "0.0.0.0", Port: 8080},
		Logging: LoggingConfig{Level: "info"},
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

	// Map OCG_DI_SERVER_PORT -> server.port, OCG_DI_LOGGING_LEVEL -> logging.level.
	transform := func(s string) string {
		return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s, envPrefix)), "_", ".")
	}
	if err := k.Load(env.Provider(envPrefix, ".", transform), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}

	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           cfg,
			WeaklyTypedInput: true,
		},
	}); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := validator.New().Struct(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}
