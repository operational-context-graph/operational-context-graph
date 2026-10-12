// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package ingest

import "context"

// Service defines the Data Ingestion domain behavior.
//
// This interface is a placeholder scaffold. Methods are added as the design is
// implemented; mockery targets this interface to generate testify mocks.
type Service interface {
	// Ping reports whether the ingestion service is operational.
	Ping(ctx context.Context) error
}

// NoopService is a placeholder Service that performs no ingestion.
type NoopService struct{}

// NewNoopService returns a Service that does nothing.
func NewNoopService() *NoopService {
	return &NoopService{}
}

// Ping always reports healthy.
func (s *NoopService) Ping(_ context.Context) error {
	return nil
}
