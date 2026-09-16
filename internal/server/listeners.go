package server

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/J0es1ick/cloud_test_assignment/internal/identity"
)

func (server *Server) configureGatewayListeners(options Options) error {
	for index, listener := range server.gateway.Config().Listeners {
		handler := server.instrument("public", server.proxyPipeline(server.gateway.Handler(listener.ID)))
		entry := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet || request.Method == http.MethodHead {
				switch request.URL.Path {
				case "/healthz":
					server.handleLiveness(writer, request)
					return
				case "/readyz":
					server.handleReadiness(writer, request)
					return
				}
			}
			handler.ServeHTTP(writer, request)
		})
		instance := newHTTPServer(listener.Address, entry, options, options.WriteTimeout)
		instance.Protocols = new(http.Protocols)
		instance.Protocols.SetHTTP1(true)
		instance.Protocols.SetHTTP2(listener.Protocol == "http2")
		instance.Protocols.SetUnencryptedHTTP2(listener.Protocol == "h2c")
		var err error
		instance.TLSConfig, err = identity.Server(listener.TLS)
		if err != nil {
			return err
		}
		if instance.TLSConfig != nil && listener.Protocol == "http1" {
			instance.TLSConfig.NextProtos = []string{"http/1.1"}
			load := instance.TLSConfig.GetConfigForClient
			instance.TLSConfig.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				current, err := load(hello)
				if err == nil {
					current.NextProtos = []string{"http/1.1"}
				}
				return current, err
			}
		}
		if index == 0 {
			server.publicServer = instance
		} else {
			server.extraServers = append(server.extraServers, instance)
		}
	}
	return nil
}

type trackedConn struct {
	net.Conn
	owner    *Server
	hijacked atomic.Bool
}

func (connection *trackedConn) Close() error {
	connection.owner.connections.Delete(connection)
	return connection.Conn.Close()
}

type trackedListener struct {
	net.Listener
	owner *Server
}

func (listener trackedListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &trackedConn{Conn: connection, owner: listener.owner}
	listener.owner.connections.Store(tracked, true)
	return tracked, nil
}

func (server *Server) connectionState(connection net.Conn, state http.ConnState) {
	if secured, ok := connection.(*tls.Conn); ok {
		connection = secured.NetConn()
	}
	if tracked, ok := connection.(*trackedConn); ok {
		if state == http.StateHijacked {
			tracked.hijacked.Store(true)
		}
		if state == http.StateClosed {
			server.connections.Delete(tracked)
		}
	}
}

func (server *Server) hijackedCount() int {
	count := 0
	server.connections.Range(func(key, value any) bool {
		if key.(*trackedConn).hijacked.Load() {
			count++
		}
		return true
	})
	return count
}
func (server *Server) closeConnections() {
	server.connections.Range(func(key, value any) bool { _ = key.(*trackedConn).Close(); return true })
}

func (server *Server) httpServers() []*http.Server {
	result := []*http.Server{server.publicServer}
	result = append(result, server.extraServers...)
	if server.managementServer != nil {
		result = append(result, server.managementServer)
	}
	if server.metricsServer != nil {
		result = append(result, server.metricsServer)
	}
	return result
}
