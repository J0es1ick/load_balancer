package config

import (
	"fmt"
	"net/url"
)

type CredentialConfig struct {
	Name               string `yaml:"name" json:"name"`
	Role               string `yaml:"role" json:"role"`
	TokenEnv           string `yaml:"token_env" json:"token_env,omitempty"`
	CertificateSubject string `yaml:"certificate_subject" json:"certificate_subject,omitempty"`
}

type ServerTLSConfig struct {
	CertFile          string `yaml:"cert_file" json:"cert_file,omitempty"`
	KeyFile           string `yaml:"key_file" json:"key_file,omitempty"`
	CAFile            string `yaml:"ca_file" json:"ca_file,omitempty"`
	RequireClientCert bool   `yaml:"require_client_cert" json:"require_client_cert,omitempty"`
}

type ClientTLSConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	CAFile         string `yaml:"ca_file" json:"ca_file,omitempty"`
	ServerName     string `yaml:"server_name" json:"server_name,omitempty"`
	ClientCertFile string `yaml:"client_cert_file" json:"client_cert_file,omitempty"`
	ClientKeyFile  string `yaml:"client_key_file" json:"client_key_file,omitempty"`
}

type TelemetryConfig struct {
	OTLPEndpoint string  `yaml:"otlp_endpoint" json:"otlp_endpoint,omitempty"`
	ServiceName  string  `yaml:"service_name" json:"service_name,omitempty"`
	SampleRate   float64 `yaml:"sample_rate" json:"sample_rate"`
	Insecure     bool    `yaml:"insecure" json:"insecure"`
}

func (cfg *Config) validateSecurity() error {
	names := make(map[string]bool)
	for _, credential := range cfg.Management.Credentials {
		if credential.Name == "" || names[credential.Name] {
			return fmt.Errorf("management credential names must be unique and nonempty")
		}
		names[credential.Name] = true
		switch credential.Role {
		case "viewer", "operator", "admin", "metrics", "discovery":
		default:
			return fmt.Errorf("invalid management role %q", credential.Role)
		}
		if credential.TokenEnv == "" && credential.CertificateSubject == "" {
			return fmt.Errorf("management credential %q needs token_env or certificate_subject", credential.Name)
		}
		if credential.CertificateSubject != "" && !cfg.Management.TLS.RequireClientCert {
			return fmt.Errorf("certificate identity requires management mutual TLS")
		}
	}
	if cfg.Management.MetricsAddress != "" && cfg.Management.MetricsAuthTokenEnv == "" {
		return fmt.Errorf("metrics_auth_token_env is required for the metrics listener")
	}
	if err := cfg.Management.TLS.Validate(); err != nil {
		return err
	}
	if cfg.Telemetry.SampleRate < 0 || cfg.Telemetry.SampleRate > 1 {
		return fmt.Errorf("telemetry.sample_rate must be between 0 and 1")
	}
	if cfg.Telemetry.OTLPEndpoint != "" {
		u, err := url.Parse(cfg.Telemetry.OTLPEndpoint)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && cfg.Telemetry.Insecure)) {
			return fmt.Errorf("telemetry.otlp_endpoint must be HTTPS (HTTP requires explicit insecure)")
		}
	}
	return nil
}

func (cfg ServerTLSConfig) Validate() error {
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return fmt.Errorf("TLS certificate and key must be configured together")
	}
	if cfg.RequireClientCert && (cfg.CAFile == "" || cfg.CertFile == "") {
		return fmt.Errorf("mutual TLS requires server certificate, key and client CA")
	}
	return nil
}
