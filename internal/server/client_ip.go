package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
)

type clientIPResolver struct{ trusted []netip.Prefix }

func newClientIPResolver(values []string) (*clientIPResolver, error) {
	resolver := &clientIPResolver{trusted: make([]netip.Prefix, 0, len(values))}
	for _, value := range values {
		prefix, err := parsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", value, err)
		}
		resolver.trusted = append(resolver.trusted, prefix)
	}
	return resolver, nil
}

func (server *Server) clientIP(request *http.Request) string {
	remoteIP, ok := parseRemoteIP(request.RemoteAddr)
	if !ok {
		return request.RemoteAddr
	}
	resolver := server.resolver.Load()
	if resolver == nil || !resolver.isTrusted(remoteIP) {
		return remoteIP.String()
	}
	if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		candidate := remoteIP
		for index := len(parts) - 1; index >= 0; index-- {
			address, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
			if err != nil {
				return remoteIP.String()
			}
			address = address.Unmap()
			candidate = address
			if !resolver.isTrusted(address) {
				return address.String()
			}
		}
		return candidate.String()
	}
	if realIP, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Real-IP"))); err == nil {
		return realIP.Unmap().String()
	}
	return remoteIP.String()
}

func (server *Server) withVerifiedClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := balancer.WithVerifiedClientIP(request.Context(), server.clientIP(request))
		ctx = balancer.WithVerifiedForwardedProto(ctx, server.clientProtocol(request))
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (server *Server) clientProtocol(request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	remoteIP, ok := parseRemoteIP(request.RemoteAddr)
	resolver := server.resolver.Load()
	if !ok || resolver == nil || !resolver.isTrusted(remoteIP) {
		return "http"
	}
	parts := strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")
	if len(parts) > 0 {
		protocol := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
		if protocol == "http" || protocol == "https" {
			return protocol
		}
	}
	return "http"
}

func (resolver *clientIPResolver) isTrusted(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseRemoteIP(remote string) (netip.Addr, bool) {
	if value, err := netip.ParseAddrPort(remote); err == nil {
		return value.Addr().Unmap(), true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func parsePrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(address, address.BitLen()), nil
}

func cloneHealth(value balancer.HealthSettings) *balancer.HealthSettings {
	value.ExpectedStatuses = append([]int(nil), value.ExpectedStatuses...)
	return &value
}
