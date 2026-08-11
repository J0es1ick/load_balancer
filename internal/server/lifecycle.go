package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/balancer"
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
	errorsChannel := make(chan error, 2)
	start := func(name string, httpServer *http.Server, listener net.Listener) {
		slog.Info("HTTP listener started", "listener", name, "address", listener.Addr().String())
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}
	go start("public", server.publicServer, publicListener)
	if server.managementServer != nil {
		go start("management", server.managementServer, managementListener)
	}
	serveError := <-errorsChannel
	if serveError != nil {
		_ = server.publicServer.Close()
		if server.managementServer != nil {
			_ = server.managementServer.Close()
		}
	}
	return serveError
}

func (server *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down HTTP listeners")
	var shutdownErrors []error
	if err := server.publicServer.Shutdown(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	if server.managementServer != nil {
		if err := server.managementServer.Shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
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
