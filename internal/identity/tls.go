package identity

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

func roots(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read certificate authority: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("certificate authority contains no certificates")
	}
	return pool, nil
}

func Server(settings config.ServerTLSConfig) (*tls.Config, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.CertFile == "" {
		return nil, nil
	}
	load := func() (*tls.Config, error) {
		certificate, err := tls.LoadX509KeyPair(settings.CertFile, settings.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load server certificate: %w", err)
		}
		value := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h2", "http/1.1"}}
		if settings.RequireClientCert {
			value.ClientAuth = tls.RequireAndVerifyClientCert
			value.ClientCAs, err = roots(settings.CAFile)
			if err != nil {
				return nil, err
			}
		}
		return value, nil
	}
	initial, err := load()
	if err != nil {
		return nil, err
	}
	initial.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return load() }
	return initial, nil
}

func Client(settings config.ClientTLSConfig) (*tls.Config, error) {
	if !settings.Enabled {
		return nil, nil
	}
	value := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: settings.ServerName}
	if settings.CAFile != "" {
		var err error
		value.RootCAs, err = roots(settings.CAFile)
		if err != nil {
			return nil, err
		}
	}
	if (settings.ClientCertFile == "") != (settings.ClientKeyFile == "") {
		return nil, fmt.Errorf("client TLS certificate and key must be configured together")
	}
	if settings.ClientCertFile != "" {
		if _, err := tls.LoadX509KeyPair(settings.ClientCertFile, settings.ClientKeyFile); err != nil {
			return nil, err
		}
		value.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			certificate, err := tls.LoadX509KeyPair(settings.ClientCertFile, settings.ClientKeyFile)
			return &certificate, err
		}
	}
	return value, nil
}
