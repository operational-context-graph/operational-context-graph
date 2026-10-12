// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

// Config holds all runtime configuration for inventory-store.
type Config struct {
	HTTPPort          string `koanf:"HTTP_PORT"            validate:"required"`
	DatabaseURL       string `koanf:"DATABASE_URL"         validate:"required"`
	DBMaxOpen         int    `koanf:"DB_MAX_OPEN_CONNS"`
	DBMaxIdle         int    `koanf:"DB_MAX_IDLE_CONNS"`
	DBConnMaxLifetime string `koanf:"DB_CONN_MAX_LIFETIME"`
	LogLevel          string `koanf:"LOG_LEVEL"`
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
	if cfg.DBMaxOpen == 0 {
		cfg.DBMaxOpen = 25
	}
	if cfg.DBMaxIdle == 0 {
		cfg.DBMaxIdle = 5
	}
	if cfg.DBConnMaxLifetime == "" {
		cfg.DBConnMaxLifetime = "5m"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if err := validator.New().Struct(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}
