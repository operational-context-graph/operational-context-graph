// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// serviceRegistrar registers gRPC services onto a grpc.Server.
type serviceRegistrar interface {
	register(s *grpc.Server)
}

// Server manages the gRPC listener lifecycle.
type Server struct {
	grpcCfg  *config.GRPCConfig
	limits   *config.LimitsConfig
	services []serviceRegistrar
	running  atomic.Bool
}

// NewServer constructs a Server. All arguments are required and must not be nil.
// Returns an error when any argument is nil.
func NewServer(grpcCfg *config.GRPCConfig, limits *config.LimitsConfig, retry *config.RetryConfig, ingestor ingest.Ingestor, schemaClient SchemaRegistryClient) (*Server, error) {
	if grpcCfg == nil {
		return nil, errors.New("grpcserver: grpcCfg must not be nil")
	}
	if limits == nil {
		return nil, errors.New("grpcserver: limits must not be nil")
	}
	if retry == nil {
		return nil, errors.New("grpcserver: retry must not be nil")
	}
	if ingestor == nil {
		return nil, errors.New("grpcserver: ingestor must not be nil")
	}
	ocgHandler, err := NewOCGHandler(ingestor, limits, retry)
	if err != nil {
		return nil, err
	}
	otlpHandler, err := newOTLPHandler(schemaClient, ingestor, retry)
	if err != nil {
		return nil, err
	}
	return &Server{
		grpcCfg:  grpcCfg,
		limits:   limits,
		services: []serviceRegistrar{ocgHandler, otlpHandler},
	}, nil
}

// Run starts the gRPC server and blocks until ctx is cancelled. It returns an
// error if another Run is already active, the listener cannot be bound, or TLS
// configuration fails.
func (srv *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", net.JoinHostPort(srv.grpcCfg.Host, strconv.Itoa(srv.grpcCfg.Port)))
	if err != nil {
		return fmt.Errorf("grpcserver: listen %s:%d: %w", srv.grpcCfg.Host, srv.grpcCfg.Port, err)
	}
	return srv.RunOnListener(ctx, lis)
}

// RunOnListener starts the gRPC server on an already-bound listener and blocks
// until ctx is cancelled. It returns an error if another Run is already active
// or TLS configuration fails. The listener is closed when Run returns.
func (srv *Server) RunOnListener(ctx context.Context, lis net.Listener) error {
	if !srv.running.CompareAndSwap(false, true) {
		_ = lis.Close()
		return errors.New("grpcserver: Run is already active on this Server")
	}
	defer srv.running.Store(false)

	// Respect already-cancelled context before serving.
	select {
	case <-ctx.Done():
		_ = lis.Close()
		return nil
	default:
	}

	opts, err := srv.serverOptions()
	if err != nil {
		return err
	}

	grpcSrv := grpc.NewServer(opts...)

	for _, svc := range srv.services {
		svc.register(grpcSrv)
	}

	services := make([]string, 0, len(grpcSrv.GetServiceInfo()))
	for name := range grpcSrv.GetServiceInfo() {
		services = append(services, name)
	}
	slices.Sort(services)
	slog.Info("grpc services registered", "services", services)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcSrv.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		srv.shutdown(grpcSrv)
		return nil
	case err := <-serveErr:
		return err
	}
}

// serverOptions builds gRPC server options from configuration.
func (srv *Server) serverOptions() ([]grpc.ServerOption, error) {
	// MaxRecvMsgSize is the sole payload size guard. Proto framing overhead
	// (field tags, length prefixes, string fields) counts against this limit,
	// so requests where overhead pushes wire size past MaxPayloadBytes are
	// transport-rejected with RESOURCE_EXHAUSTED.
	if srv.limits.MaxPayloadBytes > math.MaxInt {
		return nil, fmt.Errorf("grpcserver: limits.max_payload_bytes %d overflows int", srv.limits.MaxPayloadBytes)
	}
	maxRecv := int(srv.limits.MaxPayloadBytes)

	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxRecv),
	}

	tlsOpts, err := srv.tlsOption()
	if err != nil {
		return nil, err
	}
	if tlsOpts != nil {
		opts = append(opts, tlsOpts)
	}

	return opts, nil
}

// tlsOption returns a grpc.ServerOption for mTLS when TLS is configured,
// or nil when TLS is disabled.
func (srv *Server) tlsOption() (grpc.ServerOption, error) {
	if srv.grpcCfg.TLS == nil {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(srv.grpcCfg.TLS.CertFile, srv.grpcCfg.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpcserver: load TLS key pair: %w", err)
	}

	caPEM, err := os.ReadFile(srv.grpcCfg.TLS.CAFile)
	if err != nil {
		return nil, fmt.Errorf("grpcserver: read CA file %q: %w", srv.grpcCfg.TLS.CAFile, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("grpcserver: no valid certificates found in CA file %q", srv.grpcCfg.TLS.CAFile)
	}

	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		Certificates: []tls.Certificate{cert},
	}
	return grpc.Creds(credentials.NewTLS(tlsCfg)), nil
}

// shutdown performs a graceful stop with a hard-stop fallback after ShutdownTimeout.
//
// GracefulStop drains in-flight RPCs before returning. On timeout, Stop closes
// all transports immediately and returns — the GracefulStop goroutine is left to
// finish in the background (it will exit once transports are closed).
//
// We never call Stop() from the same goroutine as GracefulStop(): both share an
// internal mutex and GracefulStop holds it through handlersWG.Wait(), so calling
// Stop() synchronously after GracefulStop() starts would deadlock.
func (srv *Server) shutdown(grpcSrv *grpc.Server) {
	timeout := srv.grpcCfg.ShutdownTimeout

	graceDone := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(graceDone)
	}()

	select {
	case <-graceDone:
		// Drained cleanly within timeout.
	case <-time.After(timeout):
		// GracefulStop is stuck on a long-running handler. Call Stop from a
		// separate goroutine so it can acquire the mutex independently of the
		// GracefulStop goroutine and close all transports. We do not wait for
		// graceDone here — that goroutine may block in handlersWG.Wait() until
		// the stuck handler eventually returns.
		go grpcSrv.Stop()
	}
}
