package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	NetBox        NetBoxConfig        `koanf:"netbox"         validate:"required"`
	GRPC          DataIngestionConfig `koanf:"grpc"           validate:"required"`
	Endpoints     []string            `koanf:"endpoints"     validate:"required"`
	SchemaVersion string              `koanf:"schema_version" validate:"required"`
	ArtifactTypes map[string]string   `koanf:"artifact_types"`
}

type NetBoxConfig struct {
	BaseURL  string `koanf:"base_url"  validate:"required,http_url"`
	Token    string `koanf:"token"`
	PageSize int    `koanf:"page_size" validate:"min=1,max=10000"`
}

type DataIngestionConfig struct {
	Endpoint    string `koanf:"endpoint"     validate:"required"`
	CertFile    string `koanf:"cert_file"`
	KeyFile     string `koanf:"key_file"`
	ConnectorID string `koanf:"connector_id" validate:"required"`
}

var validate = validator.New()

func Load() (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(env.Provider("CONNECTOR_", ".", envKey), nil); err != nil {
		return nil, err
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	// koanf env provider treats everything as a string scalar, so slices and
	// maps must be read raw and decoded manually.
	if raw := os.Getenv("CONNECTOR_ENDPOINTS"); raw != "" {
		cfg.Endpoints = strings.Split(raw, ",")
	}
	if raw := os.Getenv("CONNECTOR_ARTIFACT_TYPES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.ArtifactTypes); err != nil {
			return nil, fmt.Errorf("CONNECTOR_ARTIFACT_TYPES: %w", err)
		}
	}

	return &cfg, validate.Struct(&cfg)
}

// envKey maps CONNECTOR_NETBOX__BASE_URL -> netbox.base_url.
// CONNECTOR_ENDPOINTS and CONNECTOR_ARTIFACT_TYPES are handled manually after
// unmarshal because koanf cannot deserialize slices or maps from flat env vars.
func envKey(s string) string {
	s = strings.TrimPrefix(s, "CONNECTOR_")
	// Exclude keys handled manually so koanf never sees them.
	if s == "ENDPOINTS" || s == "ARTIFACT_TYPES" {
		return ""
	}
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "__", ".")
}
