package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type listenerContextKey struct{}

type compiledRoute struct {
	config  config.GatewayRouteConfig
	order   int
	counter atomic.Uint64
	limiter *routeLimiter
}

func compileRoutes(values []config.GatewayRouteConfig) ([]*compiledRoute, error) {
	routes := make([]*compiledRoute, 0, len(values))
	for index, value := range values {
		route := &compiledRoute{config: value, order: index}
		if value.RateLimit != nil && value.RateLimit.Enabled {
			route.limiter = newRouteLimiter(value.RateLimit.Capacity, value.RateLimit.RefillPerSecond)
		}
		routes = append(routes, route)
	}
	sort.SliceStable(routes, func(left, right int) bool {
		if routes[left].config.Priority != routes[right].config.Priority {
			return routes[left].config.Priority > routes[right].config.Priority
		}
		leftExact := routes[left].config.Match.PathExact != ""
		rightExact := routes[right].config.Match.PathExact != ""
		if leftExact != rightExact {
			return leftExact
		}
		if len(routes[left].config.Match.PathPrefix) != len(routes[right].config.Match.PathPrefix) {
			return len(routes[left].config.Match.PathPrefix) > len(routes[right].config.Match.PathPrefix)
		}
		return routes[left].order < routes[right].order
	})
	return routes, nil
}

func withListener(request *http.Request, listenerID string) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), listenerContextKey{}, listenerID))
}

func listenerFromRequest(request *http.Request) string {
	value, _ := request.Context().Value(listenerContextKey{}).(string)
	return value
}

func (route *compiledRoute) matches(listenerID string, listener config.GatewayListenerConfig, request *http.Request) bool {
	if route.config.Listener != "" && route.config.Listener != listenerID {
		return false
	}
	host := canonicalHost(request.Host)
	if !matchesAnyHost(listener.Hostnames, host) || !matchesAnyHost(route.config.Match.Hosts, host) {
		return false
	}
	if len(route.config.Match.Methods) > 0 {
		matched := false
		for _, method := range route.config.Match.Methods {
			if strings.EqualFold(method, request.Method) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	path := request.URL.Path
	if route.config.Match.PathExact != "" && path != route.config.Match.PathExact {
		return false
	}
	if route.config.Match.PathPrefix != "" && !pathPrefixMatch(path, route.config.Match.PathPrefix) {
		return false
	}
	for _, match := range route.config.Match.Headers {
		values, present := request.Header[http.CanonicalHeaderKey(match.Name)]
		if match.Present != nil && present != *match.Present {
			return false
		}
		if match.Exact != "" {
			matched := false
			for _, value := range values {
				if value == match.Exact {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

func (route *compiledRoute) clusterID() string {
	if route.config.Action.Cluster != "" {
		return route.config.Action.Cluster
	}
	total := 0
	for _, target := range route.config.Action.WeightedClusters {
		total += target.Weight
	}
	if total == 0 {
		return ""
	}
	position := int((route.counter.Add(1) - 1) % uint64(total))
	for _, target := range route.config.Action.WeightedClusters {
		if position < target.Weight {
			return target.Cluster
		}
		position -= target.Weight
	}
	return ""
}

func (route *compiledRoute) redirect(writer http.ResponseWriter, request *http.Request) bool {
	redirect := route.config.Action.Redirect
	if redirect == nil {
		return false
	}
	target := &url.URL{Scheme: redirect.Scheme, Host: redirect.Host, Path: redirect.Path}
	if target.Scheme == "" {
		target.Scheme = request.URL.Scheme
	}
	if target.Scheme == "" {
		if request.TLS != nil {
			target.Scheme = "https"
		} else {
			target.Scheme = "http"
		}
	}
	if target.Host == "" {
		target.Host = request.Host
	}
	if target.Path == "" {
		target.Path = request.URL.Path
	}
	if redirect.PreserveQuery {
		target.RawQuery = request.URL.RawQuery
	}
	http.Redirect(writer, request, target.String(), redirect.StatusCode)
	return true
}

func (route *compiledRoute) prepareRequest(writer http.ResponseWriter, request *http.Request, clusterRetry balancer.RetryPolicy) (*http.Request, context.CancelFunc, bool, string) {
	if route.limiter != nil && !route.limiter.allow(time.Now()) {
		writer.Header().Set("Retry-After", "1")
		http.Error(writer, "Too Many Requests", http.StatusTooManyRequests)
		return nil, func() {}, false, "rate_limit"
	}
	if limit := route.config.MaxRequestBodyBytes; limit > 0 {
		if request.ContentLength > limit {
			http.Error(writer, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
			return nil, func() {}, false, "body"
		}
	}
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	applyHeaderPolicy(copy.Header, route.config.RequestHeaders)
	if prefix := route.config.Match.PathPrefix; prefix != "" && route.config.Action.RewritePrefix != "" {
		rewriteRoutePrefix(copy.URL, prefix, route.config.Action.RewritePrefix)
	}
	ctx := balancer.WithUpstreamHost(copy.Context(), route.config.Action.PreserveHost, route.config.Action.HostRewrite)
	if route.config.Retry != nil || route.config.Timeouts.PerTry.Duration() > 0 {
		policy := clusterRetry
		if route.config.Retry != nil {
			policy = retryPolicy(route.config.Retry)
		}
		if route.config.Timeouts.PerTry.Duration() > 0 {
			policy.PerTryTimeout = route.config.Timeouts.PerTry.Duration()
		}
		ctx = balancer.WithRetryPolicy(ctx, policy)
	}
	cancel := func() {}
	if timeout := route.config.Timeouts.Request.Duration(); timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	copy = copy.WithContext(ctx)
	if limit := route.config.MaxRequestBodyBytes; limit > 0 && copy.Body != nil {
		copy.Body = http.MaxBytesReader(writer, copy.Body, limit)
	}
	return copy, cancel, true, ""
}

func rewriteRoutePrefix(target *url.URL, matchedPrefix, replacement string) {
	originalPath := target.Path
	originalEscaped := target.EscapedPath()
	suffix := strings.TrimPrefix(originalPath, matchedPrefix)
	target.Path = joinRoutePath(replacement, suffix)
	target.RawPath = ""

	matchedEscaped := (&url.URL{Path: matchedPrefix}).EscapedPath()
	if !strings.HasPrefix(originalEscaped, matchedEscaped) {
		return
	}
	replacementEscaped := (&url.URL{Path: replacement}).EscapedPath()
	rawSuffix := strings.TrimPrefix(originalEscaped, matchedEscaped)
	rawPath := joinEscapedRoutePath(replacement, replacementEscaped, suffix, rawSuffix)
	decoded, err := url.PathUnescape(rawPath)
	if err == nil && decoded == target.Path {
		target.RawPath = rawPath
	}
}

func joinEscapedRoutePath(prefix, escapedPrefix, suffix, escapedSuffix string) string {
	if strings.HasSuffix(prefix, "/") && strings.HasPrefix(suffix, "/") {
		return strings.TrimSuffix(escapedPrefix, "/") + escapedSuffix
	}
	if !strings.HasSuffix(prefix, "/") && !strings.HasPrefix(suffix, "/") && suffix != "" {
		return escapedPrefix + "/" + escapedSuffix
	}
	return escapedPrefix + escapedSuffix
}

func validateIncomingRequest(request *http.Request) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("request URL is required")
	}
	if (request.URL.IsAbs() || request.URL.Host != "") && request.RequestURI != "" {
		return fmt.Errorf("absolute-form request targets are not accepted")
	}
	if strings.ContainsAny(request.Method, "\r\n\x00") || strings.ContainsAny(request.Host, "\r\n\x00") {
		return fmt.Errorf("request line contains control characters")
	}
	if request.Host != "" {
		authority, err := url.Parse("http://" + request.Host)
		if err != nil || authority.Host != request.Host || authority.Hostname() == "" || authority.User != nil || authority.Path != "" {
			return fmt.Errorf("request authority is invalid")
		}
	}
	contentLengths := request.Header.Values("Content-Length")
	if len(contentLengths) > 1 || (len(contentLengths) == 1 && strings.Contains(contentLengths[0], ",")) {
		return fmt.Errorf("ambiguous Content-Length")
	}
	if request.Header.Get("Transfer-Encoding") != "" {
		return fmt.Errorf("raw Transfer-Encoding header is not accepted")
	}
	if len(request.TransferEncoding) > 0 {
		if request.ContentLength >= 0 || len(request.TransferEncoding) != 1 || !strings.EqualFold(request.TransferEncoding[0], "chunked") {
			return fmt.Errorf("ambiguous transfer framing")
		}
	}
	return nil
}

func applyHeaderPolicy(headers http.Header, policy config.GatewayHeaderPolicy) {
	for _, name := range policy.Remove {
		headers.Del(name)
	}
	for name, value := range policy.Set {
		headers.Set(name, value)
	}
	for name, values := range policy.Add {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
}

type headerPolicyWriter struct {
	http.ResponseWriter
	policy      config.GatewayHeaderPolicy
	applied     bool
	diagnostics bool
	status      int
}

func (writer *headerPolicyWriter) WriteHeader(status int) {
	writer.apply()
	if status >= 200 && writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *headerPolicyWriter) Write(value []byte) (int, error) {
	writer.apply()
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(value)
}

func (writer *headerPolicyWriter) ReadFrom(source io.Reader) (int64, error) {
	writer.apply()
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	if readerFrom, ok := writer.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(source)
	}
	return io.Copy(writer.ResponseWriter, source)
}

func (writer *headerPolicyWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *headerPolicyWriter) apply() {
	if writer.applied {
		return
	}
	writer.applied = true
	if !writer.diagnostics {
		for name := range writer.Header() {
			if strings.HasPrefix(strings.ToLower(name), "x-balancer-") {
				writer.Header().Del(name)
			}
		}
	}
	applyHeaderPolicy(writer.Header(), writer.policy)
}

func canonicalHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(value, ".")
}

func matchesAnyHost(patterns []string, host string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		pattern = canonicalHost(pattern)
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
		}
	}
	return false
}

func pathPrefixMatch(path, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || strings.HasSuffix(prefix, "/") || path[len(prefix)] == '/'
}

func joinRoutePath(prefix, suffix string) string {
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if suffix == "" {
		return prefix
	}
	if strings.HasSuffix(prefix, "/") && strings.HasPrefix(suffix, "/") {
		return prefix + suffix[1:]
	}
	if !strings.HasSuffix(prefix, "/") && !strings.HasPrefix(suffix, "/") {
		return prefix + "/" + suffix
	}
	return prefix + suffix
}

func validateListener(snapshot *runtimeSnapshot, listenerID string) (config.GatewayListenerConfig, error) {
	if listenerID == "" {
		listenerID = snapshot.defaultListener
	}
	listener, exists := snapshot.listeners[listenerID]
	if !exists {
		return config.GatewayListenerConfig{}, fmt.Errorf("unknown gateway listener %q", listenerID)
	}
	return listener, nil
}
