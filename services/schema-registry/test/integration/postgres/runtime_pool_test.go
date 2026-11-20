package postgres_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/config/introspection"
	storage "github.com/operational-context-graph/operational-context-graph/services/schema-registry/internal/storage/postgres"
)

/*
Runtime pool lifecycle and host-matching scope

Container-backed checks require a Docker-compatible runtime.
Testcontainers provisions PostgreSQL, testify suites own its lifecycle, and Goose applies the module's migrations.
Suite methods stay serial because they share runtime roles and database state.
Container-backed checks remain outside Docker-free unit-test packages.

The suite generates temporary credentials and certificates instead of storing secrets in source files.
Certificate files use restrictive permissions.
The certificate matches the Docker host resolved by Testcontainers, including configured host overrides.
The verified-TLS fixture selects verify-full without replacing the resolved host or port.
Fixture transport settings are not service-level security requirements.

Lifecycle assertions use the public storage interface rather than retaining manually acquired connections across assertions.
Startup and role-restoration work use bounded contexts.
Teardown attempts both administrative connection closure and container termination despite individual cleanup failures.

The host-matching regression test covers Domain Name System (DNS) names and Internet Protocol (IP) versions 4 and 6.
It preserves endpoint parameters, verifies the generated certificate, and rejects a different hostname.
This certificate-and-configuration test opens no network connections and does not establish remote Docker connectivity.
*/

// runtimePoolSuite owns a disposable PostgreSQL server with Transport Layer Security (TLS) and migrated read-only grants.
// The suite keeps database startup and teardown outside Docker-free unit tests.
type runtimePoolSuite struct {
	suite.Suite
	container *tcpostgres.PostgresContainer
	admin     *pgx.Conn
	config    introspection.DatabaseConfig
}

// TestRuntimePoolSuite checks plaintext and URL-selected TLS through the public runtime-pool lifecycle.
func TestRuntimePoolSuite(t *testing.T) { suite.Run(t, new(runtimePoolSuite)) }

// SetupSuite creates host-matching certificates, starts PostgreSQL, and applies Goose migrations within a bounded deadline.
func (s *runtimePoolSuite) SetupSuite() {
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	provider, err := testcontainers.NewDockerProvider()
	require.NoError(t, err)
	defer func() { require.NoError(t, provider.Close()) }()
	host, err := provider.DaemonHost(ctx)
	require.NoError(t, err)
	caPath, certPath, keyPath := runtimeTLSFiles(t, host)
	directory := filepath.Dir(caPath)
	configPath := filepath.Join(directory, "postgresql.conf")
	// TLS 1.2-only fixtures catch any service-level TLS 1.3 pin while also accepting plaintext clients.
	require.NoError(t, os.WriteFile(configPath, []byte("listen_addresses='*'\nssl=on\nssl_min_protocol_version='TLSv1.2'\nssl_max_protocol_version='TLSv1.2'\nssl_ca_file='/tmp/testcontainers-go/postgres/ca_cert.pem'\nssl_cert_file='/tmp/testcontainers-go/postgres/server.cert'\nssl_key_file='/tmp/testcontainers-go/postgres/server.key'\n"), 0o600))
	// Credentials exist only for this disposable server and never enter source files.
	password := make([]byte, 32)
	_, err = rand.Read(password)
	require.NoError(t, err)
	s.container, err = tcpostgres.Run(ctx, "postgres:18", tcpostgres.WithDatabase("schema_registry"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword(hex.EncodeToString(password)),
		tcpostgres.WithConfigFile(configPath), tcpostgres.WithSSLCert(caPath, certPath, keyPath),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	require.NoError(t, err)
	databaseURL, err := s.container.ConnectionString(ctx)
	require.NoError(t, err)
	s.config = introspection.DatabaseConfig{URL: verifiedRuntimeURL(t, databaseURL, caPath), PoolMaxConnections: 4}
	s.admin, err = pgx.Connect(ctx, s.config.URL)
	require.NoError(t, err)
	database, err := sql.Open("pgx", s.config.URL)
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, database, migrationDirectory()))
}

// TearDownSuite closes the administrative connection and terminates the container despite individual cleanup failures.
func (s *runtimePoolSuite) TearDownSuite() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.admin != nil {
		s.NoError(s.admin.Close(ctx))
	}
	if s.container != nil {
		s.NoError(s.container.Terminate(ctx))
	}
}

// TestOpenReadyStoreAndClose checks service-owned pool wiring with plaintext and verified TLS 1.2 connections.
// Both transports cover startup, readiness without an active release, a store read, and shared pool closure.
func (s *runtimePoolSuite) TestOpenReadyStoreAndClose() {
	for _, mode := range []string{"disable", "verify-full"} {
		s.Run("sslmode="+mode, func() {
			t := s.T()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			target, err := url.Parse(s.config.URL)
			require.NoError(t, err)

			parameters := target.Query()
			parameters.Set("sslmode", mode)
			applicationName := "runtime-lifecycle-" + rand.Text()
			parameters.Set("application_name", applicationName)
			target.RawQuery = parameters.Encode()
			cfg := s.config
			cfg.URL = target.String()
			pool, err := storage.Open(ctx, cfg)
			require.NoError(t, err)

			defer pool.Close()
			require.NoError(t, pool.Ready(ctx))

			store, err := pool.Store(storage.Timeouts{
				Acquire: time.Second, Query: time.Second, Statement: time.Second,
				Lock: time.Second, IdleInTransaction: time.Second,
			})
			require.NoError(t, err)
			require.ErrorIs(t, store.Read(ctx, func(context.Context, *storage.Snapshot) error {
				assert.Fail(t, "empty registry must not invoke the read callback")
				return nil
			}), storage.ErrNoActiveRelease)
			s.assertRuntimeTransport(ctx, applicationName, mode == "verify-full")

			pool.Close()
			require.ErrorContains(t, pool.Ready(ctx), "acquire readiness connection")
			require.ErrorContains(t, store.Read(ctx, func(context.Context, *storage.Snapshot) error { return nil }), "acquire read connection")
		})
	}
}

// assertRuntimeTransport verifies the actual server-observed transport rather than inferring it from URL configuration.
func (s *runtimePoolSuite) assertRuntimeTransport(ctx context.Context, applicationName string, encrypted bool) {
	s.T().Helper()
	var connections int
	var transportMatches, allTLS12 bool
	err := s.admin.QueryRow(ctx, `
		SELECT count(*), coalesce(bool_and(ssl = $2), false), coalesce(bool_and(version = 'TLSv1.2'), false)
		FROM pg_stat_activity JOIN pg_stat_ssl USING (pid)
		WHERE application_name = $1`, applicationName, encrypted).Scan(&connections, &transportMatches, &allTLS12)
	s.Require().NoError(err)
	s.Positive(connections)
	s.True(transportMatches)
	if encrypted {
		s.True(allTLS12)
	}
}

// TestRequestedVerificationFailure checks that the runtime pool retains the user's certificate trust policy.
func (s *runtimePoolSuite) TestRequestedVerificationFailure() {
	t := s.T()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	target, err := url.Parse(s.config.URL)
	require.NoError(t, err)

	otherCA, _, _ := runtimeTLSFiles(t, target.Hostname())
	parameters := target.Query()
	parameters.Set("sslrootcert", otherCA)
	target.RawQuery = parameters.Encode()
	cfg := s.config
	cfg.URL = target.String()
	pool, err := storage.Open(ctx, cfg)
	require.ErrorContains(t, err, "verify database connection")

	var certificateErr x509.UnknownAuthorityError
	require.ErrorAs(t, err, &certificateErr)
	require.Nil(t, pool)
}

// TestOpenRejectsMissingRuntimeRole temporarily renames the migrated runtime role while preserving its grants.
// Startup must fail before returning a pool, and deferred cleanup restores the role.
func (s *runtimePoolSuite) TestOpenRejectsMissingRuntimeRole() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Rename rather than drop the migrated role, preserving its grants.
	_, err := s.admin.Exec(ctx, "ALTER ROLE schema_registry_api RENAME TO unavailable_registry_api")
	s.Require().NoError(err)
	defer func() {
		restoreCtx, cancelRestore := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRestore()
		_, restoreErr := s.admin.Exec(restoreCtx, "ALTER ROLE unavailable_registry_api RENAME TO schema_registry_api")
		s.Require().NoError(restoreErr)
	}()
	pool, err := storage.Open(ctx, s.config)
	s.Require().ErrorContains(err, "assume read-only database role")
	s.Nil(pool)
}

// verifiedRuntimeURL enables certificate verification without replacing Testcontainers' host, port, or existing parameters.
func verifiedRuntimeURL(t testing.TB, databaseURL, caPath string) string {
	t.Helper()
	target, err := url.Parse(databaseURL)
	require.NoError(t, err)
	query := target.Query()
	query.Set("sslmode", "verify-full")
	query.Set("sslrootcert", caPath)
	// Preserve Testcontainers' authority; certificate matching must not redirect the network connection.
	target.RawQuery = query.Encode()
	return target.String()
}

// runtimeTLSFiles creates an ephemeral certificate authority and a server certificate for the resolved Docker host.
// Temporary files use restrictive permissions; standard-library cryptography generates all keys and signatures.
func runtimeTLSFiles(t testing.TB, host string) (string, string, string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "runtime-pool-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if address := net.ParseIP(host); address != nil {
		certificate.IPAddresses = []net.IP{address}
	} else {
		certificate.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	directory := t.TempDir()
	caPath, certPath, keyPath := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "server.pem"), filepath.Join(directory, "server.key")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return caPath, certPath, keyPath
}

// TestRuntimeTLSMatchesResolvedHost covers Domain Name System (DNS) names and Internet Protocol (IP) version 4 and 6 addresses.
// Each case preserves the endpoint, verifies its generated certificate, and rejects a different hostname.
// This certificate-and-configuration test starts no container and contacts no remote host.
func TestRuntimeTLSMatchesResolvedHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"docker.example.test", "192.0.2.10", "2001:db8::10"} {
		t.Run(host, func(t *testing.T) {
			caPath, certPath, _ := runtimeTLSFiles(t, host)
			original := url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, "5432"), User: url.User("registry"), Path: "/schema_registry", RawQuery: "application_name=runtime-test"}
			target, err := url.Parse(verifiedRuntimeURL(t, original.String(), caPath))
			require.NoError(t, err)
			require.Equal(t, original.Host, target.Host, "TLS settings must preserve Testcontainers' network endpoint")
			require.Equal(t, "runtime-test", target.Query().Get("application_name"))
			require.Equal(t, "verify-full", target.Query().Get("sslmode"))
			require.Equal(t, caPath, target.Query().Get("sslrootcert"))
			// #nosec G304 -- runtimeTLSFiles creates this fixed-name file under t.TempDir.
			caPEM, err := os.ReadFile(caPath)
			require.NoError(t, err)
			roots := x509.NewCertPool()
			require.True(t, roots.AppendCertsFromPEM(caPEM))
			// #nosec G304 -- runtimeTLSFiles creates this fixed-name file under t.TempDir.
			certPEM, err := os.ReadFile(certPath)
			require.NoError(t, err)
			block, _ := pem.Decode(certPEM)
			require.NotNil(t, block)
			certificate, err := x509.ParseCertificate(block.Bytes)
			require.NoError(t, err)
			_, err = certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: target.Hostname()})
			require.NoError(t, err)
			_, err = certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: "wrong.example.test"})
			require.Error(t, err)
		})
	}
}
