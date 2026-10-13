package config

import (
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	NetBox    NetBoxConfig    `koanf:"netbox"    validate:"required"`
	GRPC      DataIngestionConfig `koanf:"grpc"      validate:"required"`
	Connector ConnectorConfig `koanf:"connector"`
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

type ConnectorConfig struct {
	Endpoints            []string          `koanf:"endpoints"`
	LogicalArtifactIds map[string]string `koanf:"logical_artifact_ids"`
	SchemaVersion        string            `koanf:"schema_version" validate:"required"`
}

var validate = validator.New()

func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, err
	}

	if err := k.Load(env.Provider("CONNECTOR_", ".", envKey), nil); err != nil {
		return nil, err
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	return &cfg, validate.Struct(&cfg)
}

// envKey maps CONNECTOR_NETBOX__BASE_URL -> netbox.base_url
func envKey(s string) string {
	s = strings.TrimPrefix(s, "CONNECTOR_")
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "__", ".")
}
