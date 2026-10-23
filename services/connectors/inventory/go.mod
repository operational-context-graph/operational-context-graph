module github.com/operational-context-graph/operational-context-graph/services/connectors/inventory

go 1.26.0

toolchain go1.27.3

require (
	github.com/go-playground/validator/v10 v10.30.5
	github.com/knadh/koanf/providers/env v1.1.0
	github.com/knadh/koanf/providers/env/v2 v2.0.2
	github.com/knadh/koanf/v2 v2.4.1
	github.com/operational-context-graph/operational-context-graph/gen/go v0.0.0
	github.com/stretchr/testify v1.12.1
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/gabriel-vasile/mimetype v1.4.15 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261012220534-4341edf3f941 // indirect
)

replace github.com/operational-context-graph/operational-context-graph/gen/go => ../../../gen/go
