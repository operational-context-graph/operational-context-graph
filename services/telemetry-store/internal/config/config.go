// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"
)

// Config holds all runtime configuration for telemetry-store.
type Config struct {
	HTTPPort           string `koanf:"HTTP_PORT"              validate:"required"`
	DorisDSN           string `koanf:"DORIS_DSN"              validate:"required"`
	LogLevel           string `koanf:"LOG_LEVEL"`
	DorisBuckets       string `koanf:"DORIS_BUCKETS"`
	DorisReplicationNum string `koanf:"DORIS_REPLICATION_NUM"`
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
	if err := validator.New().Struct(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}
