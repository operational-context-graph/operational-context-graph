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
	"net"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// Server manages the gRPC listener lifecycle.
type Server struct {
	grpcCfg  *config.GRPCConfig
	limits   *config.LimitsConfig
	retry    *config.RetryConfig
	ingestor ingest.Ingestor
	running  atomic.Bool
}

// NewServer constructs a Server. Returns an error when any argument is nil.
func NewServer(grpcCfg *config.GRPCConfig, limits *config.LimitsConfig, retry *config.RetryConfig, ingestor ingest.Ingestor) (*Server, error) {
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
	return &Server{grpcCfg: grpcCfg, limits: limits, retry: retry, ingestor: ingestor}, nil
}

// Run starts the gRPC server and blocks until ctx is cancelled. It returns an
// error if another Run is already active, the listener cannot be bound, or TLS
// configuration fails.
func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("grpcserver: Run is already active on this Server")
	}
	defer s.running.Store(false)

	// Respect already-cancelled context before binding.
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	opts, err := s.serverOptions()
	if err != nil {
		return err
	}

	grpcSrv := grpc.NewServer(opts...)

	handler, err := NewIngestionServiceServer(s.ingestor, s.limits, s.retry)
	if err != nil {
		return fmt.Errorf("grpcserver: build handler: %w", err)
	}
	ingestionv1.RegisterIngestionServiceServer(grpcSrv, handler)

	services := make([]string, 0, len(grpcSrv.GetServiceInfo()))
	for name := range grpcSrv.GetServiceInfo() {
		services = append(services, name)
	}
	slices.Sort(services)
	slog.Info("grpc services registered", "services", services)

	lis, err := net.Listen("tcp", net.JoinHostPort(s.grpcCfg.Host, strconv.Itoa(s.grpcCfg.Port)))
	if err != nil {
		return fmt.Errorf("grpcserver: listen %s:%d: %w", s.grpcCfg.Host, s.grpcCfg.Port, err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcSrv.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		s.shutdown(grpcSrv)
		return nil
	case err := <-serveErr:
		return err
	}
}

// serverOptions builds gRPC server options from configuration.
func (s *Server) serverOptions() ([]grpc.ServerOption, error) {
	// MaxRecvMsgSize is the sole payload size guard. Proto framing overhead
	// (field tags, length prefixes, string fields) counts against this limit,
	// so requests where overhead pushes wire size past MaxPayloadBytes are
	// transport-rejected with RESOURCE_EXHAUSTED — same code as the old
	// application-layer check.
	maxRecv := int(s.limits.MaxPayloadBytes)

	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxRecv),
	}

	tlsOpts, err := s.tlsOption()
	if err != nil {
		return nil, err
	}
	if tlsOpts != nil {
		opts = append(opts, tlsOpts)
	}

	return opts, nil
}

// tlsOption returns a grpc.ServerOption for mTLS when all three TLS paths are
// set, or nil when none are set. Partial configuration is rejected by config
// validation before Run is called, so we only handle both-or-neither here.
func (s *Server) tlsOption() (grpc.ServerOption, error) {
	if s.grpcCfg.TLSCertFile == "" {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(s.grpcCfg.TLSCertFile, s.grpcCfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpcserver: load TLS key pair: %w", err)
	}

	caPEM, err := os.ReadFile(s.grpcCfg.TLSCAFile)
	if err != nil {
		return nil, fmt.Errorf("grpcserver: read CA file %q: %w", s.grpcCfg.TLSCAFile, err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("grpcserver: no valid certificates found in CA file %q", s.grpcCfg.TLSCAFile)
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
func (s *Server) shutdown(grpcSrv *grpc.Server) {
	timeout := s.grpcCfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

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
