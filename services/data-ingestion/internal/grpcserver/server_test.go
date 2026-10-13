// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	ingestionv1 "github.com/operational-context-graph/operational-context-graph/gen/go/ocg/ingestion/v1"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/config"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/grpcserver"
	"github.com/operational-context-graph/operational-context-graph/services/data-ingestion/internal/ingest"
)

// --- helpers ---

// freeHostPort allocates a free TCP port on 127.0.0.1 and returns host and port separately.
func freeHostPort(t *testing.T) (host, port string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	h, p, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	l.Close()
	return h, p
}

// freeAddr returns a free address as "host:port" for use with dial helpers.
func freeAddr(t *testing.T) string {
	t.Helper()
	host, port := freeHostPort(t)
	return net.JoinHostPort(host, port)
}

func defaultGRPCCfg(host, port string) *config.GRPCConfig {
	p, _ := strconv.Atoi(port)
	return &config.GRPCConfig{
		Host:            host,
		Port:            p,
		ShutdownTimeout: 5 * time.Second,
	}
}

func newTestServer(t *testing.T, grpcCfg *config.GRPCConfig) *grpcserver.Server {
	t.Helper()
	srv, err := grpcserver.NewServer(grpcCfg, defaultLimits(), defaultRetry(), noop())
	require.NoError(t, err)
	return srv
}

// startServer runs the server in a goroutine and returns a cancel func and
// error channel. Call cancel() to trigger shutdown.
func startServer(srv *grpcserver.Server) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- srv.Run(ctx)
	}()
	return cancel, errc
}

// waitReady polls until the gRPC server accepts a plaintext connection.
func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", addr)
}

// --- TLS test fixture helpers ---

type tlsFixture struct {
	dir         string
	caCertFile  string
	serverCert  tls.Certificate
	serverCA    *x509.CertPool
	clientCert  tls.Certificate
	clientCA    *x509.CertPool
	caKey       *ecdsa.PrivateKey
	caCert      *x509.Certificate
	caCertBytes []byte
}

func generateCA(t *testing.T) (key *ecdsa.PrivateKey, cert *x509.Certificate, certPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err = x509.ParseCertificate(certDER)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return
}

func issueCert(t *testing.T, caKey *ecdsa.PrivateKey, caCert *x509.Certificate, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-peer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return tlsCert
}

func writePEMFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, data, 0600))
	return p
}

func buildTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	dir := t.TempDir()

	caKey, caCert, caCertPEM := generateCA(t)

	// Write CA cert to file
	caCertFile := writePEMFile(t, dir, "ca.pem", caCertPEM)

	// Server cert signed by CA
	serverTLSCert := issueCert(t, caKey, caCert, time.Now().Add(24*time.Hour))
	serverCertPEM, serverKeyPEM := certKeyPEM(t, serverTLSCert)
	writePEMFile(t, dir, "server.crt", serverCertPEM)
	writePEMFile(t, dir, "server.key", serverKeyPEM)

	// Client cert signed by CA
	clientTLSCert := issueCert(t, caKey, caCert, time.Now().Add(24*time.Hour))

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	return &tlsFixture{
		dir:         dir,
		caCertFile:  caCertFile,
		serverCert:  serverTLSCert,
		serverCA:    caPool,
		clientCert:  clientTLSCert,
		clientCA:    caPool,
		caKey:       caKey,
		caCert:      caCert,
		caCertBytes: caCertPEM,
	}
}

func certKeyPEM(t *testing.T, c tls.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	require.NoError(t, err)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	ecKey, ok := c.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok)
	keyDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

func tlsGRPCCfg(t *testing.T, host, port string, fix *tlsFixture) *config.GRPCConfig {
	t.Helper()
	p, _ := strconv.Atoi(port)
	return &config.GRPCConfig{
		Host:            host,
		Port:            p,
		TLSCertFile:     filepath.Join(fix.dir, "server.crt"),
		TLSKeyFile:      filepath.Join(fix.dir, "server.key"),
		TLSCAFile:       fix.caCertFile,
		ShutdownTimeout: 5 * time.Second,
	}
}

// --- 8.2: port already in use → error immediately ---

func TestNewServerNilGRPCCfg(t *testing.T) {
	_, err := grpcserver.NewServer(nil, defaultLimits(), defaultRetry(), noop())
	require.Error(t, err)
}

func TestNewServerNilLimits(t *testing.T) {
	host, port := freeHostPort(t)
	_, err := grpcserver.NewServer(defaultGRPCCfg(host, port), nil, defaultRetry(), noop())
	require.Error(t, err)
}

func TestNewServerNilRetry(t *testing.T) {
	host, port := freeHostPort(t)
	_, err := grpcserver.NewServer(defaultGRPCCfg(host, port), defaultLimits(), nil, noop())
	require.Error(t, err)
}

func TestNewServerNilIngestor(t *testing.T) {
	host, port := freeHostPort(t)
	_, err := grpcserver.NewServer(defaultGRPCCfg(host, port), defaultLimits(), defaultRetry(), nil)
	require.Error(t, err)
}

func TestRunPortAlreadyInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	host, port, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	srv := newTestServer(t, defaultGRPCCfg(host, port))

	ctx := context.Background()
	err = srv.Run(ctx)
	require.Error(t, err)
}

// --- 8.3: already-cancelled context → returns without starting ---

func TestRunAlreadyCancelledContext(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	srv := newTestServer(t, defaultGRPCCfg(host, port))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := srv.Run(ctx)
	require.NoError(t, err)

	// Confirm nothing is listening
	conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatal("server should not be listening after cancelled context")
	}
}

// --- 8.4: second concurrent Run → error ---

func TestRunReentrancy(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	srv := newTestServer(t, defaultGRPCCfg(host, port))

	cancel, errc := startServer(srv)
	defer cancel()
	waitReady(t, addr)

	// Second Run must fail immediately.
	ctx2 := context.Background()
	err := srv.Run(ctx2)
	require.Error(t, err)

	cancel()
	require.NoError(t, <-errc)
}

// --- 8.5: mTLS scenarios ---

func dialInsecure(t *testing.T, addr string) (*grpc.ClientConn, error) {
	t.Helper()
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func dialMTLS(t *testing.T, addr string, clientCert tls.Certificate, caPool *x509.CertPool) (*grpc.ClientConn, error) {
	t.Helper()
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS13,
	})
	return grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
}

func TestMTLSValidClientAccepted(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	fix := buildTLSFixture(t)
	srv := newTestServer(t, tlsGRPCCfg(t, host, port, fix))

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	conn, err := dialMTLS(t, addr, fix.clientCert, fix.clientCA)
	require.NoError(t, err)
	defer conn.Close()

	client := ingestionv1.NewIngestionServiceClient(conn)
	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()

	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{{
			LogicalArtifactId: "a",
			SchemaVersion:     "v1",
			Payload:           []byte("hello"),
		}},
	})
	require.NoError(t, err)
}

func TestMTLSNoClientCertRejected(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	fix := buildTLSFixture(t)
	srv := newTestServer(t, tlsGRPCCfg(t, host, port, fix))

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	// Connect without a client cert — should fail at TLS handshake.
	creds := credentials.NewTLS(&tls.Config{
		RootCAs:    fix.clientCA,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS13,
	})
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()
	client := ingestionv1.NewIngestionServiceClient(conn)
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{{Payload: []byte("x")}},
	})
	require.Error(t, err)
}

func TestMTLSWrongCARejected(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	fix := buildTLSFixture(t)
	srv := newTestServer(t, tlsGRPCCfg(t, host, port, fix))

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	// Generate a different CA and issue a client cert from it.
	otherKey, otherCACert, _ := generateCA(t)
	wrongCert := issueCert(t, otherKey, otherCACert, time.Now().Add(24*time.Hour))

	conn, err := dialMTLS(t, addr, wrongCert, fix.clientCA)
	require.NoError(t, err)
	defer conn.Close()

	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()
	client := ingestionv1.NewIngestionServiceClient(conn)
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{{Payload: []byte("x")}},
	})
	require.Error(t, err)
}

func TestMTLSExpiredClientCertRejected(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	fix := buildTLSFixture(t)
	srv := newTestServer(t, tlsGRPCCfg(t, host, port, fix))

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	expiredCert := issueCert(t, fix.caKey, fix.caCert, time.Now().Add(-time.Hour))

	conn, err := dialMTLS(t, addr, expiredCert, fix.clientCA)
	require.NoError(t, err)
	defer conn.Close()

	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()
	client := ingestionv1.NewIngestionServiceClient(conn)
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{{Payload: []byte("x")}},
	})
	require.Error(t, err)
}

// --- 8.6: invalid CA file → Run returns error ---

func TestRunInvalidCAFile(t *testing.T) {
	host, port := freeHostPort(t)
	p, _ := strconv.Atoi(port)
	dir := t.TempDir()

	fix := buildTLSFixture(t)
	caFile := filepath.Join(dir, "bad-ca.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("not-pem"), 0600))

	grpcCfg := &config.GRPCConfig{
		Host:            host,
		Port:            p,
		TLSCertFile:     filepath.Join(fix.dir, "server.crt"),
		TLSKeyFile:      filepath.Join(fix.dir, "server.key"),
		TLSCAFile:       caFile,
		ShutdownTimeout: 5 * time.Second,
	}
	srv := newTestServer(t, grpcCfg)
	err := srv.Run(context.Background())
	require.Error(t, err)
}

func TestRunMissingCertFile(t *testing.T) {
	host, port := freeHostPort(t)
	p, _ := strconv.Atoi(port)
	fix := buildTLSFixture(t)

	grpcCfg := &config.GRPCConfig{
		Host:            host,
		Port:            p,
		TLSCertFile:     "/nonexistent/server.crt",
		TLSKeyFile:      filepath.Join(fix.dir, "server.key"),
		TLSCAFile:       fix.caCertFile,
		ShutdownTimeout: 5 * time.Second,
	}
	srv := newTestServer(t, grpcCfg)
	err := srv.Run(context.Background())
	require.Error(t, err)
}

// TestRunMissingCAFile — cert/key load OK, CA file missing (os.ReadFile error).
func TestRunMissingCAFile(t *testing.T) {
	host, port := freeHostPort(t)
	p, _ := strconv.Atoi(port)
	fix := buildTLSFixture(t)

	grpcCfg := &config.GRPCConfig{
		Host:            host,
		Port:            p,
		TLSCertFile:     filepath.Join(fix.dir, "server.crt"),
		TLSKeyFile:      filepath.Join(fix.dir, "server.key"),
		TLSCAFile:       "/nonexistent/ca.pem",
		ShutdownTimeout: 5 * time.Second,
	}
	srv := newTestServer(t, grpcCfg)
	err := srv.Run(context.Background())
	require.Error(t, err)
}

// TestShutdownZeroTimeout — ShutdownTimeout==0 uses default 30s path in shutdown().
func TestShutdownZeroTimeout(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	p, _ := strconv.Atoi(port)

	srv, err := grpcserver.NewServer(
		&config.GRPCConfig{Host: host, Port: p, ShutdownTimeout: 0},
		defaultLimits(), defaultRetry(), noop(),
	)
	require.NoError(t, err)

	cancel, errc := startServer(srv)
	waitReady(t, addr)
	cancel()
	require.NoError(t, <-errc)
}

// --- 8.7: plaintext mode log ---

func TestPlaintextModeStarts(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	srv := newTestServer(t, defaultGRPCCfg(host, port))

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	conn, err := dialInsecure(t, addr)
	require.NoError(t, err)
	defer conn.Close()

	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()
	client := ingestionv1.NewIngestionServiceClient(conn)
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{{
			LogicalArtifactId: "a",
			SchemaVersion:     "v1",
			Payload:           []byte("hello"),
		}},
	})
	require.NoError(t, err)
}

// --- 8.8: graceful shutdown — in-flight RPC completes ---

func TestGracefulShutdownDrainsRPC(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	p, _ := strconv.Atoi(port)
	start := make(chan struct{})
	done := make(chan struct{})

	srvObj := newTestServer(t, defaultGRPCCfg(host, port))
	// Override ingestor with one that signals when it starts and waits for done.
	slowIngestor := &fakeIngestor{fn: func(ctx context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		close(start)
		select {
		case <-done:
		case <-ctx.Done():
		}
		return ingest.IngestResult{Accepted: 1}, nil
	}}
	slowSrv, err := grpcserver.NewServer(
		&config.GRPCConfig{Host: host, Port: p, ShutdownTimeout: 5 * time.Second},
		defaultLimits(), defaultRetry(), slowIngestor,
	)
	require.NoError(t, err)
	_ = srvObj

	cancel, errc := startServer(slowSrv)
	waitReady(t, addr)

	conn, err := dialInsecure(t, addr)
	require.NoError(t, err)
	defer conn.Close()

	rpcDone := make(chan error, 1)
	go func() {
		ctx2, cl := context.WithTimeout(context.Background(), 10*time.Second)
		defer cl()
		client := ingestionv1.NewIngestionServiceClient(conn)
		_, e := client.Ingest(ctx2, &ingestionv1.IngestRequest{
			Messages: []*ingestionv1.MessageEnvelope{{Payload: []byte("x")}},
		})
		rpcDone <- e
	}()

	// Wait for RPC to be in-flight.
	<-start

	// Cancel context — triggers graceful stop.
	cancel()

	// Allow slow ingestor to finish.
	close(done)

	// RPC should complete without error.
	require.NoError(t, <-rpcDone)
	// Run should return nil.
	require.NoError(t, <-errc)
}

// --- 8.9: hard-stop after ShutdownTimeout ---

func TestHardStopAfterShutdownTimeout(t *testing.T) {
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)
	p, _ := strconv.Atoi(port)
	stuck := make(chan struct{})

	stuckIngestor := &fakeIngestor{fn: func(ctx context.Context, _ []ingest.IngestRecord) (ingest.IngestResult, error) {
		close(stuck)
		// Block until the test is done.
		select {}
	}}

	srv, err := grpcserver.NewServer(
		&config.GRPCConfig{Host: host, Port: p, ShutdownTimeout: 200 * time.Millisecond},
		defaultLimits(), defaultRetry(), stuckIngestor,
	)
	require.NoError(t, err)

	cancel, errc := startServer(srv)
	waitReady(t, addr)

	conn, err := dialInsecure(t, addr)
	require.NoError(t, err)
	defer conn.Close()

	// Fire an RPC that will block.
	go func() {
		ctx2, cl := context.WithTimeout(context.Background(), 10*time.Second)
		defer cl()
		client := ingestionv1.NewIngestionServiceClient(conn)
		_, _ = client.Ingest(ctx2, &ingestionv1.IngestRequest{
			Messages: []*ingestionv1.MessageEnvelope{{Payload: []byte("x")}},
		})
	}()

	<-stuck
	cancel()

	start := time.Now()
	require.NoError(t, <-errc)
	elapsed := time.Since(start)
	// Run must return within ShutdownTimeout + 500ms safety margin.
	assert.Less(t, elapsed, 700*time.Millisecond)
}

// --- 8.10: MaxRecvMsgSize rejects oversized messages at the transport layer ---
//
// MaxRecvMsgSize == MaxPayloadBytes. Proto framing (field tags, length prefixes)
// counts against the limit, so a payload smaller than MaxPayloadBytes is needed
// to leave room for framing. A payload at maxPayload itself pushes the wire size
// over the limit and gets transport-rejected with RESOURCE_EXHAUSTED.

func TestMaxRecvMsgSizeRejectsOversized(t *testing.T) {
	const maxPayload = 1024
	limits := &config.LimitsConfig{MaxMessages: 10, MaxPayloadBytes: maxPayload}
	host, port := freeHostPort(t)
	addr := net.JoinHostPort(host, port)

	srv, err := grpcserver.NewServer(
		defaultGRPCCfg(host, port), limits, defaultRetry(), noop(),
	)
	require.NoError(t, err)

	cancel, errc := startServer(srv)
	defer func() {
		cancel()
		<-errc
	}()
	waitReady(t, addr)

	conn, err := dialInsecure(t, addr)
	require.NoError(t, err)
	defer conn.Close()

	ctx, cl := context.WithTimeout(context.Background(), 3*time.Second)
	defer cl()
	client := ingestionv1.NewIngestionServiceClient(conn)

	// Small payload — fits within MaxRecvMsgSize including framing overhead.
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{Payload: make([]byte, 100)},
		},
	})
	require.NoError(t, err)

	// Payload at maxPayload — wire size exceeds MaxRecvMsgSize due to framing;
	// transport rejects with RESOURCE_EXHAUSTED.
	_, err = client.Ingest(ctx, &ingestionv1.IngestRequest{
		Messages: []*ingestionv1.MessageEnvelope{
			{Payload: make([]byte, maxPayload)},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}
