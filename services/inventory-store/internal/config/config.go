// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

// envPrefix namespaces environment variables for this service.
// Double-underscore (__) separates hierarchy levels; single underscore is part of a field name.
// For example OCG_IS_DATABASE__HOST maps to the database.host key.
const envPrefix = "OCG_IS_"

// Config holds all runtime configuration for inventory-store.
type Config struct {
	Server   ServerConfig   `koanf:"server"`
	Logging  LoggingConfig  `koanf:"logging"`
	Database DatabaseConfig `koanf:"database"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port int `koanf:"port" validate:"required,min=1,max=65535"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level string `koanf:"level" validate:"required,oneof=debug info warn error"`
}

// DatabaseConfig holds PostgreSQL connection settings. The password is
// supplied only via OCG_IS_DATABASE__PASSWORD (from a Secret).
type DatabaseConfig struct {
	Host            string `koanf:"host"              validate:"required"`
	Port            int    `koanf:"port"              validate:"required,min=1,max=65535"`
	Name            string `koanf:"name"              validate:"required"`
	User            string `koanf:"user"              validate:"required"`
	Password        string `koanf:"password"          validate:"required"`
	SSLMode         string `koanf:"sslmode"           validate:"required,oneof=disable allow prefer require verify-ca verify-full"`
	MaxConns        int    `koanf:"maxconns"          validate:"min=0"`
	ConnMaxLifetime string `koanf:"conn_max_lifetime"`
}

// DSN builds a pgx connection string from the Database settings.
func (c *Config) DSN() string {
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

// Load reads configuration from OCG_IS_* environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Server:  ServerConfig{Port: 8080},
		Logging: LoggingConfig{Level: "info"},
		Database: DatabaseConfig{
			Host:            "localhost",
			Port:            5432,
			Name:            "ocg",
			User:            "ocg",
			SSLMode:         "disable",
			MaxConns:        25,
			ConnMaxLifetime: "5m",
		},
	}

	k := koanf.New(".")
	transform := func(key, val string) (string, any) {
		key = strings.TrimPrefix(key, envPrefix)
		key = strings.ToLower(key)
		return strings.ReplaceAll(key, "__", "."), val
	}
	if err := k.Load(env.Provider(".", env.Opt{Prefix: envPrefix, TransformFunc: transform}), nil); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}
	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := validator.New().Struct(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}
