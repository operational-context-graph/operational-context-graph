package publisher

import (
	"context"
	"crypto/tls"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func dialTLS(endpoint, certFile, keyFile string) (*grpc.ClientConn, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
}

func outCtx(ctx context.Context, connectorID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "connector-id", connectorID)
}
