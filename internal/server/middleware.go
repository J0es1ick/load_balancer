package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"go.opentelemetry.io/otel/attribute"
)

type contextKey string

const requestIDKey contextKey = "request-id"

const managementCSRFHeader = "X-Balancer-CSRF"

func (server *Server) managementAuth(token string, insecure bool, next http.Handler) http.Handler {
	credentials := append([]Credential{}, server.credentials...)
	if token != "" {
		credentials = append(credentials, Credential{Name: "admin", Role: "admin", Token: func() (string, error) { return token, nil }})
	}
	return server.authorize(credentials, insecure, next)
}

func (server *Server) managementMutationGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/") || request.Method == http.MethodGet || request.Method == http.MethodHead {
			next.ServeHTTP(writer, request)
			return
		}
		if strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site") {
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cross-site management mutations are forbidden"})
			return
		}
		if request.Header.Get(managementCSRFHeader) != "1" {
			writeJSON(writer, http.StatusForbidden, map[string]string{"error": "management mutation requires X-Balancer-CSRF: 1"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "management mutation requires Content-Type: application/json"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) instrument(listener string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if listener == "public" {
			var end func()
			request, end = traceIncoming(request)
			defer end()
		}
		if requestID == "" || len(requestID) > 128 {
			requestID = newRequestID()
		}
		writer.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		request = request.WithContext(ctx)
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(recorder, request)
		duration := time.Since(started)
		if server.metrics != nil {
			server.metrics.ObserveHTTPRequest(listener, recorder.status, duration)
		}
		if shouldLogAccess(listener, request.Method, recorder.status, server.accessLogSampleRate) {
			slog.InfoContext(ctx, "HTTP request", "request_id", requestID, "trace_id", observability.TraceID(ctx), "listener", listener, "method", request.Method, "path", accessLogPath(request.URL.Path, server.accessLogIncludePath), "status", recorder.status, "duration_ms", duration.Milliseconds(), "client_ip", server.clientIP(request))
		}
	})
}

func traceIncoming(request *http.Request) (*http.Request, func()) {
	request, span := observability.TraceRequest(request)
	span.SetAttributes(attribute.String("proxy.listener", "public"))
	return request, func() { span.End() }
}

func accessLogPath(path string, include bool) string {
	if !include {
		return "[redacted]"
	}
	return path
}

func shouldLogAccess(listener, method string, status int, sampleRate float64) bool {
	if status >= http.StatusBadRequest || (listener == "management" && method != http.MethodGet && method != http.MethodHead) {
		return true
	}
	return sampleRate >= 1 || (sampleRate > 0 && mathrand.Float64() < sampleRate)
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		recorder.ResponseWriter.WriteHeader(status)
		return
	}
	if recorder.wroteHeader {
		return
	}
	recorder.wroteHeader = true
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(value []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(value)
}

func (recorder *statusRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err == nil {
		return hex.EncodeToString(buffer)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
