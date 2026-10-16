// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
//
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVerifiedTLS13PeerValidation(t *testing.T) {
	certificateAuthority, trustedServer, expiredServer := tlsFixtures(t)
	rootPath := filepath.Join(t.TempDir(), "root.pem")
	require.NoError(t, os.WriteFile(rootPath, certificateAuthority, 0o600))

	t.Run("trusted peer", func(t *testing.T) {
		listener := tlsListener(t, trustedServer)
		defer func() { require.NoError(t, listener.Close()) }()
		config, err := configurePool("postgres://registry:password@localhost/registry?sslmode=verify-full&sslrootcert="+rootPath, 2, 1)
		require.NoError(t, err)
		connection, err := tls.Dial("tcp", listener.Addr().String(), config.ConnConfig.TLSConfig)
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	})

	t.Run("plaintext peer", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { require.NoError(t, listener.Close()) }()
		go acceptAndClose(listener)
		config := verifiedTLSConfig(t, rootPath, "localhost")
		connection, err := tls.Dial("tcp", listener.Addr().String(), config)
		require.Error(t, err)
		if connection != nil {
			require.NoError(t, connection.Close())
		}
	})

	t.Run("wrong host", func(t *testing.T) {
		listener := tlsListener(t, trustedServer)
		defer func() { require.NoError(t, listener.Close()) }()
		connection, err := tls.Dial("tcp", listener.Addr().String(), verifiedTLSConfig(t, rootPath, "wrong.example.test"))
		require.Error(t, err)
		if connection != nil {
			require.NoError(t, connection.Close())
		}
	})

	t.Run("unknown authority", func(t *testing.T) {
		listener := tlsListener(t, trustedServer)
		defer func() { require.NoError(t, listener.Close()) }()
		connection, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "localhost", RootCAs: x509.NewCertPool()})
		require.Error(t, err)
		if connection != nil {
			require.NoError(t, connection.Close())
		}
	})

	t.Run("expired certificate", func(t *testing.T) {
		listener := tlsListener(t, expiredServer)
		defer func() { require.NoError(t, listener.Close()) }()
		connection, err := tls.Dial("tcp", listener.Addr().String(), verifiedTLSConfig(t, rootPath, "localhost"))
		require.Error(t, err)
		if connection != nil {
			require.NoError(t, connection.Close())
		}
	})
}

func verifiedTLSConfig(t *testing.T, rootPath, serverName string) *tls.Config {
	t.Helper()
	config, err := configurePool("postgres://registry:password@localhost/registry?sslmode=verify-full&sslrootcert="+rootPath, 2, 1)
	require.NoError(t, err)
	copy := config.ConnConfig.TLSConfig.Clone()
	copy.ServerName = serverName
	return copy
}

func tlsListener(t *testing.T, certificate tls.Certificate) net.Listener {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}})
	require.NoError(t, err)
	go acceptAndClose(listener)
	return listener
}

func acceptAndClose(listener net.Listener) {
	connection, err := listener.Accept()
	if err == nil {
		if tlsConnection, ok := connection.(*tls.Conn); ok {
			_ = tlsConnection.Handshake()
		}
		_ = connection.Close()
	}
}

func tlsFixtures(t *testing.T) ([]byte, tls.Certificate, tls.Certificate) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	ca := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return caPEM, signedServerCertificate(t, ca, caKey, false), signedServerCertificate(t, ca, caKey, true)
}

func signedServerCertificate(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, expired bool) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	notBefore, notAfter := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if expired {
		notBefore, notAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	}
	certificate := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificate, ca, &key.PublicKey, caKey)
	require.NoError(t, err)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	result, err := tls.X509KeyPair(certificatePEM, keyPEM)
	require.NoError(t, err)
	return result
}
