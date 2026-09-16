package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
	"github.com/J0es1ick/cloud_test_assignment/internal/identity"
)

func newHTTPServer(address string, handler http.Handler, options Options, writeTimeout time.Duration) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: options.ReadHeaderTimeout, ReadTimeout: options.ReadTimeout, WriteTimeout: writeTimeout, IdleTimeout: options.IdleTimeout, MaxHeaderBytes: options.MaxHeaderBytes}
}

func (server *Server) Start() error {
	publicListener, err := net.Listen("tcp", server.publicServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on public address %s: %w", server.publicServer.Addr, err)
	}
	var managementListener net.Listener
	if server.managementServer != nil {
		managementListener, err = net.Listen("tcp", server.managementServer.Addr)
		if err != nil {
			_ = publicListener.Close()
			return fmt.Errorf("listen on management address %s: %w", server.managementServer.Addr, err)
		}
	}
	return server.Serve(publicListener, managementListener)
}

func (server *Server) Serve(publicListener, managementListener net.Listener) error {
	if publicListener == nil {
		return fmt.Errorf("public listener is required")
	}
	if server.managementServer != nil && managementListener == nil {
		return fmt.Errorf("management listener is required")
	}
	if server.managementServer != nil {
		var err error
		server.managementServer.TLSConfig, err = identity.Server(server.managementTLS)
		if err != nil {
			_ = publicListener.Close()
			_ = managementListener.Close()
			return err
		}
	}
	servers := server.httpServers()
	listeners := []net.Listener{publicListener}
	for _, instance := range server.extraServers {
		listener, err := net.Listen("tcp", instance.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			if managementListener != nil {
				_ = managementListener.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}
	if managementListener != nil {
		listeners = append(listeners, managementListener)
	}
	if server.metricsServer != nil {
		listener, err := net.Listen("tcp", server.metricsServer.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}
	errorsChannel := make(chan error, len(servers))
	start := func(name string, httpServer *http.Server, listener net.Listener) {
		slog.Info("HTTP listener started", "listener", name, "address", listener.Addr().String())
		httpServer.ConnState = server.connectionState
		tracked := trackedListener{Listener: listener, owner: server}
		var err error
		if httpServer.TLSConfig != nil {
			err = httpServer.ServeTLS(tracked, "", "")
		} else {
			err = httpServer.Serve(tracked)
		}
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}
	for index, instance := range servers {
		go start(instance.Addr, instance, listeners[index])
	}
	serveError := <-errorsChannel
	if serveError != nil {
		for _, instance := range servers {
			_ = instance.Close()
		}
		server.closeConnections()
	}
	return serveError
}

func (server *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down HTTP listeners")
	server.shuttingDown.Store(true)
	var shutdownErrors []error
	var mu sync.Mutex
	var group sync.WaitGroup
	for _, instance := range server.httpServers() {
		group.Add(1)
		go func(instance *http.Server) {
			defer group.Done()
			if err := instance.Shutdown(ctx); err != nil {
				mu.Lock()
				shutdownErrors = append(shutdownErrors, err)
				mu.Unlock()
				_ = instance.Close()
			}
		}(instance)
	}
	group.Wait()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for server.hijackedCount() > 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			server.closeConnections()
			return errors.Join(append(shutdownErrors, ctx.Err())...)
		}
	}
	return errors.Join(shutdownErrors...)
}

func (server *Server) UpdateRuntime(trustedProxies []string, health balancer.HealthSettings) error {
	resolver, err := newClientIPResolver(trustedProxies)
	if err != nil {
		return err
	}
	server.resolver.Store(resolver)
	server.health.Store(cloneHealth(health))
	return nil
}

func (server *Server) PublicHandler() http.Handler { return server.publicServer.Handler }

func (server *Server) ManagementHandler() http.Handler {
	if server.managementServer == nil {
		return nil
	}
	return server.managementServer.Handler
}

func (server *Server) Handler() http.Handler {
	if server.managementServer != nil {
		return server.managementServer.Handler
	}
	return server.publicServer.Handler
}
