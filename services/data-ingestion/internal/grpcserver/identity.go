// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const connectorIDHeader = "connector-id"

// connectorIDFromContext extracts the connector identity from a gRPC request
// context. The mTLS certificate CN takes priority; the connector-id metadata
// header is used when no mTLS certificate is present. Returns an empty string
// when no identity can be found.
func connectorIDFromContext(ctx context.Context) string {
	// mTLS certificate CN takes priority.
	if p, ok := peer.FromContext(ctx); ok && p.AuthInfo != nil {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			if len(tlsInfo.State.PeerCertificates) > 0 {
				cn := tlsInfo.State.PeerCertificates[0].Subject.CommonName
				if cn != "" {
					return cn
				}
			}
		}
	}

	// Fall back to connector-id metadata header.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(connectorIDHeader); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}

	return ""
}
