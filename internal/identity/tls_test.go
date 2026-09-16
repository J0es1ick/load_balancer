package identity_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCertificate(t *testing.T, certificatePath, keyPath string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	encodedKey, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey}), 0600))
}

func TestTLSCertificatesReloadOnNewHandshakes(t *testing.T) {
	directory := t.TempDir()
	certificatePath, keyPath := filepath.Join(directory, "cert.pem"), filepath.Join(directory, "key.pem")
	writeCertificate(t, certificatePath, keyPath, 1)
	listener, err := identity.Server(config.ServerTLSConfig{CertFile: certificatePath, KeyFile: keyPath, RequireClientCert: true, CAFile: certificatePath})
	require.NoError(t, err)
	client, err := identity.Client(config.ClientTLSConfig{Enabled: true, CAFile: certificatePath, ServerName: "localhost", ClientCertFile: certificatePath, ClientKeyFile: keyPath})
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), listener.MinVersion)
	assert.Equal(t, tls.RequireAndVerifyClientCert, listener.ClientAuth)
	assert.False(t, client.InsecureSkipVerify)
	writeCertificate(t, certificatePath, keyPath, 2)
	rotated, err := listener.GetConfigForClient(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rotated.Certificates[0].Leaf.SerialNumber.Int64())
	clientCertificate, err := client.GetClientCertificate(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), clientCertificate.Leaf.SerialNumber.Int64())
	require.NoError(t, os.WriteFile(keyPath, []byte("broken"), 0600))
	_, err = listener.GetConfigForClient(nil)
	require.Error(t, err, "invalid rotation must fail closed")
	_, err = client.GetClientCertificate(nil)
	require.Error(t, err)
}

func TestTLSRejectsInvalidConfiguration(t *testing.T) {
	_, err := identity.Server(config.ServerTLSConfig{CertFile: "missing"})
	require.Error(t, err)
	_, err = identity.Server(config.ServerTLSConfig{RequireClientCert: true})
	require.Error(t, err)
	_, err = identity.Client(config.ClientTLSConfig{Enabled: true, ClientCertFile: "missing"})
	require.Error(t, err)
	_, err = identity.Client(config.ClientTLSConfig{Enabled: true, CAFile: "missing"})
	require.Error(t, err)
}
