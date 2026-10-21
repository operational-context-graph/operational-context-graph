// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import "context"

// SchemaKey identifies one immutable schema release by its logical artifact type
// and version, as defined in the Schema Registry.
type SchemaKey struct {
	LogicalArtifactID string
	Version           string
}

// SchemaRegistryClient is the subset of the Schema Registry client needed by the
// OTLP receiver. Implementations MUST be safe for concurrent use by multiple goroutines.
type SchemaRegistryClient interface {
	DefaultLogSchema(ctx context.Context) (SchemaKey, error)
	DefaultMetricSchema(ctx context.Context) (SchemaKey, error)
	DefaultTraceSchema(ctx context.Context) (SchemaKey, error)
}

// NoopSchemaRegistryClient returns hard-coded default SchemaKeys per signal type.
// It is a placeholder until the real Schema Registry client (#274) is implemented.
// The names mirror the POC defaults; they must be aligned with the Schema Registry
// before the real client is wired.
type NoopSchemaRegistryClient struct{}

var _ SchemaRegistryClient = (*NoopSchemaRegistryClient)(nil)

func (*NoopSchemaRegistryClient) DefaultLogSchema(_ context.Context) (SchemaKey, error) {
	return SchemaKey{LogicalArtifactID: "ocg.otlp.logs", Version: "1.0.0"}, nil
}

func (*NoopSchemaRegistryClient) DefaultMetricSchema(_ context.Context) (SchemaKey, error) {
	return SchemaKey{LogicalArtifactID: "ocg.otlp.metrics", Version: "1.0.0"}, nil
}

func (*NoopSchemaRegistryClient) DefaultTraceSchema(_ context.Context) (SchemaKey, error) {
	return SchemaKey{LogicalArtifactID: "ocg.otlp.traces", Version: "1.0.0"}, nil
}
