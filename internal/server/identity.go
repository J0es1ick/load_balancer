package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type Credential struct {
	Name               string
	Role               string
	Token              func() (string, error)
	CertificateSubject string
}

type Principal struct {
	Name string `json:"name"`
	Role string `json:"role"`
}
type principalKey struct{}

type AuditEvent struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Subject  string    `json:"subject"`
	Role     string    `json:"role"`
	Method   string    `json:"method"`
	Resource string    `json:"resource"`
	Before   string    `json:"before"`
	After    string    `json:"after"`
	Status   int       `json:"status"`
}

type auditLog struct {
	mu     sync.Mutex
	events []AuditEvent
}

func CredentialsFromConfig(values []config.CredentialConfig) []Credential {
	credentials := make([]Credential, 0, len(values))
	for _, value := range values {
		entry := Credential{Name: value.Name, Role: value.Role, CertificateSubject: value.CertificateSubject}
		if value.TokenEnv != "" {
			name := value.TokenEnv
			entry.Token = func() (string, error) { return config.SecretFromEnv(name) }
		}
		credentials = append(credentials, entry)
	}
	return credentials
}

func principal(ctx context.Context) Principal {
	value, _ := ctx.Value(principalKey{}).(Principal)
	return value
}

func authorized(role string, request *http.Request) bool {
	path := request.URL.Path
	if role == "admin" {
		return true
	}
	if role == "discovery" {
		return (request.Method == http.MethodPut && strings.HasPrefix(path, "/api/v1/discovery/")) || (request.Method == http.MethodGet && (path == "/healthz" || path == "/readyz"))
	}
	if path == "/metrics" || path == "/healthz" || path == "/readyz" {
		return request.Method == http.MethodGet
	}
	if role == "metrics" || strings.HasPrefix(path, "/debug/") {
		return false
	}
	if strings.HasPrefix(path, "/api/v1/discovery/") {
		return false
	}
	if path == "/api/dashboard/request" && role == "viewer" {
		return false
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return role == "viewer" || role == "operator"
	}
	if path == "/api/v1/config/validate" {
		return role == "viewer" || role == "operator"
	}
	if role != "operator" {
		return false
	}
	if request.Method == http.MethodPost && path == "/api/v1/request" {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return request.Method == http.MethodPatch && len(parts) == 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "clusters" && parts[3] != "" && parts[4] == "endpoints" && parts[5] != ""
}

func (server *Server) authorize(credentials []Credential, insecure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identity := Principal{}
		if insecure {
			identity = Principal{Name: "insecure-local", Role: "admin"}
		}
		provided := sha256.Sum256([]byte(request.Header.Get("Authorization")))
		for _, credential := range credentials {
			matched := false
			if credential.Token != nil {
				token, err := credential.Token()
				if err == nil && token != "" {
					expected := sha256.Sum256([]byte("Bearer " + token))
					matched = subtle.ConstantTimeCompare(provided[:], expected[:]) == 1
				}
			}
			if credential.CertificateSubject != "" && request.TLS != nil && len(request.TLS.VerifiedChains) > 0 {
				certificate := request.TLS.VerifiedChains[0][0]
				matched = matched || certificate.Subject.String() == credential.CertificateSubject
				for _, uri := range certificate.URIs {
					matched = matched || uri.String() == credential.CertificateSubject
				}
			}
			if matched {
				identity = Principal{Name: credential.Name, Role: credential.Role}
				break
			}
		}
		if identity.Name == "" {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="load-balancer-management"`)
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), principalKey{}, identity))
		if !authorized(identity.Role, request) {
			server.recordAudit(request, http.StatusForbidden, "", "")
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "role does not permit this operation"})
			return
		}
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			next.ServeHTTP(writer, request)
			return
		}
		before := server.auditFingerprint()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		server.recordAudit(request, recorder.status, before, server.auditFingerprint())
	})
}

func (server *Server) auditFingerprint() string {
	if server.gateway == nil {
		return "legacy"
	}
	state := server.gateway.Status()
	type endpointState struct {
		Cluster  string
		ID       string
		Enabled  bool
		Draining bool
	}
	value := struct {
		Hash      string
		Endpoints []endpointState
		RateLimit any
	}{Hash: state.Hash, RateLimit: server.limiter.Settings()}
	for _, cluster := range state.Clusters {
		for _, endpoint := range cluster.Endpoints {
			value.Endpoints = append(value.Endpoints, endpointState{cluster.ID, endpoint.ID, endpoint.Enabled, endpoint.Draining})
		}
	}
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (server *Server) recordAudit(request *http.Request, status int, before, after string) {
	identity := principal(request.Context())
	event := AuditEvent{ID: newRequestID(), Time: time.Now().UTC(), Subject: identity.Name, Role: identity.Role, Method: request.Method, Resource: request.URL.Path, Before: before, After: after, Status: status}
	server.audit.mu.Lock()
	if len(server.audit.events) >= 128 {
		server.audit.events = server.audit.events[1:]
	}
	server.audit.events = append(server.audit.events, event)
	server.audit.mu.Unlock()
	slog.InfoContext(request.Context(), "management audit", "event_id", event.ID, "subject", event.Subject, "role", event.Role, "method", event.Method, "resource", event.Resource, "before", before, "after", after, "status", status)
}

func (server *Server) auditEvents() []AuditEvent {
	server.audit.mu.Lock()
	defer server.audit.mu.Unlock()
	return append([]AuditEvent{}, server.audit.events...)
}
