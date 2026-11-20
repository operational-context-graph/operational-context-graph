/*
Pool configuration and readiness unit-test scope

Configuration tests verify driver-selected transport settings without opening database connections.
The readiness cancellation test uses net.Pipe and a minimal PostgreSQL startup stub.
The stub authenticates a synthetic connection without contacting a PostgreSQL server.
The driver tracer cancels SELECT 1 after acquisition but before the query reaches the stub.
Assertions distinguish a query-stage failure from an acquisition-stage failure and require zero acquired connections afterward.

Users select database SSL transport through connection URL parameters.
Docker-backed lifecycle checks belong to test/integration/postgres/runtime_pool_test.go, not this unit-test file.
The pool closes before the test waits for stub workers, preventing cleanup from racing further connection attempts.
*/

package postgres

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
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
)

// TestConfigurePoolPreservesSSLModes compares primary and fallback transports with unmodified pgx parsing.
func TestConfigurePoolPreservesSSLModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		t.Run("sslmode="+mode, func(t *testing.T) {
			t.Parallel()
			databaseURL := "postgres://registry@db.example.test,other.example.test/registry"
			if mode != "" {
				databaseURL += "?sslmode=" + mode
			}
			assertPoolTransportMatchesDriver(t, databaseURL)
		})
	}
}

// TestConfigurePoolPreservesSSLParameters compares certificate, negotiation, SNI, and protocol parameters with the driver.
func TestConfigurePoolPreservesSSLParameters(t *testing.T) {
	t.Parallel()
	certPath, keyPath := sslFixtureFiles(t)
	for _, test := range []struct {
		name       string
		parameters url.Values
	}{
		{name: "certificates", parameters: url.Values{
			"sslmode": {"verify-full"}, "sslrootcert": {certPath}, "sslcert": {certPath}, "sslkey": {keyPath},
		}},
		{name: "direct negotiation and protocol parameters", parameters: url.Values{
			"sslmode": {"prefer"}, "sslnegotiation": {"direct"}, "sslsni": {"0"},
			"ssl_min_protocol_version": {"TLSv1.2"}, "ssl_max_protocol_version": {"TLSv1.3"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertPoolTransportMatchesDriver(t, "postgres://registry@localhost/registry?"+test.parameters.Encode())
		})
	}
}

// assertPoolTransportMatchesDriver checks that pool preparation changes no primary or fallback transport settings.
func assertPoolTransportMatchesDriver(t *testing.T, databaseURL string) {
	t.Helper()
	want, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	got, err := configurePool(databaseURL, 1, 4)
	require.NoError(t, err)
	assert.Equal(t, databaseURL, got.ConnConfig.ConnString(), "pool configuration must preserve every URL parameter")
	assert.Equal(t, want.ConnConfig.Host, got.ConnConfig.Host)
	assert.Equal(t, want.ConnConfig.Port, got.ConnConfig.Port)
	assert.Equal(t, want.ConnConfig.SSLNegotiation, got.ConnConfig.SSLNegotiation)
	assertTLSConfigMatchesDriver(t, want.ConnConfig.TLSConfig, got.ConnConfig.TLSConfig)
	require.Len(t, got.ConnConfig.Fallbacks, len(want.ConnConfig.Fallbacks))
	for index, fallback := range want.ConnConfig.Fallbacks {
		assert.Equal(t, fallback.Host, got.ConnConfig.Fallbacks[index].Host)
		assert.Equal(t, fallback.Port, got.ConnConfig.Fallbacks[index].Port)
		assertTLSConfigMatchesDriver(t, fallback.TLSConfig, got.ConnConfig.Fallbacks[index].TLSConfig)
	}
	want.ConnConfig.RuntimeParams["search_path"] = "schema_registry,pg_catalog"
	assert.Equal(t, want.ConnConfig.RuntimeParams, got.ConnConfig.RuntimeParams)
	assert.Equal(t, int32(1), got.MinConns)
	assert.Equal(t, int32(4), got.MaxConns)
	require.NotNil(t, got.PrepareConn)
}

// assertTLSConfigMatchesDriver compares TLS settings and verification callbacks without invoking a network peer.
func assertTLSConfigMatchesDriver(t *testing.T, want, got *tls.Config) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	assert.Equal(t, want.MinVersion, got.MinVersion)
	assert.Equal(t, want.MaxVersion, got.MaxVersion)
	assert.Equal(t, want.InsecureSkipVerify, got.InsecureSkipVerify)
	assert.Equal(t, want.ServerName, got.ServerName)
	assert.Equal(t, want.NextProtos, got.NextProtos)
	assert.True(t, want.RootCAs.Equal(got.RootCAs), "driver trust roots must remain unchanged")
	assert.True(t, want.ClientCAs.Equal(got.ClientCAs), "driver client authorities must remain unchanged")
	assert.Equal(t, want.Certificates, got.Certificates)
	assert.Equal(t, want.VerifyPeerCertificate == nil, got.VerifyPeerCertificate == nil)
	assert.Equal(t, want.VerifyConnection == nil, got.VerifyConnection == nil)
	assert.Equal(t, want.GetClientCertificate == nil, got.GetClientCertificate == nil)
}

// TestConfigurePoolRolePreparation checks role preparation and search-path safety independently of plaintext transport.
func TestConfigurePoolRolePreparation(t *testing.T) {
	t.Parallel()
	config, err := configurePool("postgres://registry@db.example.test/registry?sslmode=disable&search_path=public", 0, 4)
	require.NoError(t, err)
	require.Nil(t, config.ConnConfig.TLSConfig)
	require.NotNil(t, config.PrepareConn)
	assert.Equal(t, "schema_registry,pg_catalog", config.ConnConfig.RuntimeParams["search_path"])
}

// sslFixtureFiles creates a temporary self-signed certificate and key for driver configuration tests, without opening connections.
func sslFixtureFiles(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pool-config-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	directory := t.TempDir()
	certPath, keyPath := filepath.Join(directory, "client.pem"), filepath.Join(directory, "client.key")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certPath, keyPath
}

// cancellingProbeTracer cancels only the readiness statement after the pool acquires a connection.
type cancellingProbeTracer struct{ cancel context.CancelFunc }

// TraceQueryStart cancels the readiness probe at execution to exercise post-acquisition cleanup.
func (tracer cancellingProbeTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL == "SELECT 1" {
		tracer.cancel()
	}
	return ctx
}

// TraceQueryEnd completes the tracer contract without changing the injected cancellation result.
func (cancellingProbeTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestCancelledReadinessQueryReleasesConnection cancels an acquired connection's readiness query through the driver tracer.
// An in-process protocol stub keeps this unit test independent of Docker and PostgreSQL servers.
// The test requires the cancellation cause, the query-stage error, and zero acquired connections afterward.
func TestCancelledReadinessQueryReleasesConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig("postgres://registry@localhost/registry?sslmode=disable")
	require.NoError(t, err)
	config.MaxConns = 1
	config.ConnConfig.Tracer = cancellingProbeTracer{cancel: cancel}
	var servers sync.WaitGroup
	// An in-process PostgreSQL startup stub supplies a real pgx connection.
	// The tracer cancels readiness before its SELECT reaches the wire.
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		servers.Go(func() {
			defer func() { _ = server.Close() }()
			// Assertions stay non-fatal inside workers; the parent owns fatal failure decisions.
			if !assert.NoError(t, server.SetDeadline(time.Now().Add(5*time.Second))) {
				return
			}
			backend := pgproto3.NewBackend(server, server)
			if _, startupErr := backend.ReceiveStartupMessage(); startupErr != nil {
				// Failed startup also makes the parent's readiness assertions fail.
				return
			}
			backend.Send(&pgproto3.AuthenticationOk{})
			backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			if flushErr := backend.Flush(); flushErr != nil {
				return
			}
			// Cancellation may close the pipe without a termination message.
			_, _ = backend.Receive()
		})
		return client, nil
	}
	// Wait only after pool closure prevents further connection attempts.
	defer servers.Wait()
	raw, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	pool := &Pool{pool: raw}
	defer pool.Close()
	err = pool.Ready(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "execute readiness query")
	require.Zero(t, raw.Stat().AcquiredConns())
}

// TestConfigurePoolRetainsDriverSSLErrors keeps invalid modes and certificate-loading failures at the driver boundary.
func TestConfigurePoolRetainsDriverSSLErrors(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing.pem")
	for _, parameters := range []url.Values{
		{"sslmode": {"unsupported"}},
		{"sslmode": {"verify-full"}, "sslrootcert": {missing}},
	} {
		databaseURL := "postgres://registry@localhost/registry?" + parameters.Encode()
		_, driverErr := pgxpool.ParseConfig(databaseURL)
		require.Error(t, driverErr)
		config, err := configurePool(databaseURL, 0, 4)
		require.ErrorContains(t, err, driverErr.Error())
		require.Nil(t, config)
	}
}

// TestOpenRejectsMalformedConfiguration rejects an invalid database target before constructing a runtime pool.
func TestOpenRejectsMalformedConfiguration(t *testing.T) {
	t.Parallel()
	pool, err := Open(context.Background(), introspection.DatabaseConfig{URL: "://invalid"})
	require.ErrorContains(t, err, "parse database configuration")
	require.Nil(t, pool)
}

// TestOpenRetainsConnectionFailures checks unavailable and cancelled connections still fail without returning a pool.
func TestOpenRetainsConnectionFailures(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	t.Run("unavailable", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		pool, openErr := Open(ctx, introspection.DatabaseConfig{
			URL: "postgres://registry@" + address + "/registry?sslmode=disable", PoolMaxConnections: 2,
		})
		require.ErrorContains(t, openErr, "verify database connection")
		require.Nil(t, pool)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool, err := Open(ctx, introspection.DatabaseConfig{
		URL: "postgres://registry@" + address + "/registry?sslmode=disable", PoolMaxConnections: 2,
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, pool)
}
