package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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
)

type contextKey string

const requestIDKey contextKey = "request-id"

const managementCSRFHeader = "X-Balancer-CSRF"

func (server *Server) managementAuth(token string, insecure bool, next http.Handler) http.Handler {
	if insecure {
		return next
	}
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided := []byte(request.Header.Get("Authorization"))
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="load-balancer-management"`)
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) managementMutationGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/dashboard/") || request.Method == http.MethodGet || request.Method == http.MethodHead {
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
			slog.InfoContext(ctx, "HTTP request", "request_id", requestID, "listener", listener, "method", request.Method, "path", accessLogPath(request.URL.Path, server.accessLogIncludePath), "status", recorder.status, "duration_ms", duration.Milliseconds(), "client_ip", server.clientIP(request))
		}
	})
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
	if recorder.wroteHeader {
		return
	}
	recorder.wroteHeader = true
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
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
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
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
