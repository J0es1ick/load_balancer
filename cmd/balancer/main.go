package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/J0es1ick/test-assignment/internal/balancer"
	"github.com/J0es1ick/test-assignment/internal/config"
	"github.com/J0es1ick/test-assignment/internal/ratelimit"
	"github.com/J0es1ick/test-assignment/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Printf("Balancer stopped with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.InitConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backendPool, err := balancer.NewBackendPool(cfg.Backends)
	if err != nil {
		return fmt.Errorf("create backend pool: %w", err)
	}
	loadBalancer := balancer.NewLoadBalancer(backendPool, balancer.NewRoundRobinStrategy())

	rootContext, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()

	database, err := ratelimit.NewDatabase(cfg)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("Database close error: %v", err)
		}
	}()

	if err := database.Init(rootContext); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	database.StartCleanupWorker(rootContext, 6*time.Hour)

	limiter := ratelimit.NewTokenBucketLimiter(
		cfg.RateLimit.DefaultCapacity,
		cfg.RateLimit.DefaultRate,
		database,
	)

	healthChecker := balancer.NewHealthChecker(
		backendPool,
		cfg.HealthCheck.Interval,
		cfg.HealthCheck.Timeout,
	)
	healthChecker.Check(rootContext)
	go healthChecker.Start(rootContext)

	httpServer, err := server.NewServer(serverOptions(cfg), loadBalancer, limiter)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.Start()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)

	currentConfig := cfg
	running := true
	for running {
		select {
		case signalValue := <-signals:
			if signalValue == syscall.SIGHUP {
				nextConfig, err := config.InitConfig()
				if err != nil {
					log.Printf("Config reload rejected: %v", err)
					continue
				}
				if err := applyReload(
					rootContext,
					currentConfig,
					nextConfig,
					backendPool,
					healthChecker,
					limiter,
					httpServer,
				); err != nil {
					log.Printf("Config reload rejected: %v", err)
					continue
				}
				currentConfig = nextConfig
				log.Println("Configuration reloaded")
				continue
			}
			log.Printf("Received %s, shutting down", signalValue)
			running = false
		case err := <-serverErrors:
			if err != nil {
				return fmt.Errorf("HTTP server: %w", err)
			}
			running = false
		}
	}

	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		currentConfig.Server.ShutdownTimeout,
	)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}

	stopWorkers()
	log.Println("Server gracefully stopped")
	return nil
}

func applyReload(
	ctx context.Context,
	currentConfig *config.Config,
	nextConfig *config.Config,
	backendPool *balancer.BackendPool,
	healthChecker *balancer.HealthChecker,
	limiter *ratelimit.TokenBucketLimiter,
	httpServer *server.Server,
) error {
	if err := config.ValidateReload(currentConfig, nextConfig); err != nil {
		return err
	}

	if _, err := balancer.NewBackendPool(nextConfig.Backends); err != nil {
		return err
	}
	if err := limiter.Reconfigure(
		ctx,
		nextConfig.RateLimit.DefaultCapacity,
		nextConfig.RateLimit.DefaultRate,
	); err != nil {
		return fmt.Errorf("reconfigure rate limiter: %w", err)
	}
	if err := backendPool.ReplaceBackends(nextConfig.Backends); err != nil {
		return fmt.Errorf("replace backends: %w", err)
	}
	if err := healthChecker.Update(
		nextConfig.HealthCheck.Interval,
		nextConfig.HealthCheck.Timeout,
	); err != nil {
		return err
	}
	if err := httpServer.UpdateRuntime(
		nextConfig.Server.TrustedProxies,
		nextConfig.HealthCheck.Interval,
		nextConfig.HealthCheck.Timeout,
	); err != nil {
		return err
	}
	healthChecker.Check(ctx)
	return nil
}

func serverOptions(cfg *config.Config) server.Options {
	return server.Options{
		Port:              cfg.Server.Port,
		TrustedProxies:    cfg.Server.TrustedProxies,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
		HealthInterval:    cfg.HealthCheck.Interval,
		HealthTimeout:     cfg.HealthCheck.Timeout,
	}
}
