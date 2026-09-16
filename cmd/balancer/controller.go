package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/discovery"
	"github.com/J0es1ick/cloud_test_assignment/internal/identity"
	"github.com/J0es1ick/cloud_test_assignment/internal/server"
)

type discoveryDelivery struct {
	cluster string
	update  server.DiscoveryUpdate
}

type discoveryState struct {
	Good    *server.DiscoveryUpdate
	Failure *server.DiscoveryUpdate
}

func (previous discoveryState) observe(update server.DiscoveryUpdate) discoveryState {
	if update.Error != "" {
		previous.Failure = &update
	} else {
		previous.Good = &update
		previous.Failure = nil
	}
	return previous
}

func discoveryTargets(value string, allowPlaintext bool) ([]*url.URL, error) {
	values := strings.Split(value, ",")
	targets := make([]*url.URL, 0, len(values))
	for _, value := range values {
		u, err := url.Parse(strings.TrimSpace(value))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("DISCOVERY_TARGETS must contain management HTTP(S) origins")
		}
		if u.Scheme == "http" && !allowPlaintext {
			return nil, fmt.Errorf("discovery requires HTTPS; plaintext isolated development needs DISCOVERY_ALLOW_PLAINTEXT=true")
		}
		targets = append(targets, u)
	}
	return targets, nil
}

func runDiscoveryController(cfg *config.Config) error {
	if cfg.Gateway == nil {
		return fmt.Errorf("discovery controller requires gateway configuration")
	}
	targets, err := discoveryTargets(os.Getenv("DISCOVERY_TARGETS"), os.Getenv("DISCOVERY_ALLOW_PLAINTEXT") == "true")
	if err != nil {
		return err
	}
	if _, err := config.SecretFromEnv("BALANCER_DISCOVERY_TOKEN"); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	events := make(chan discoveryDelivery, 32)
	latest := make(map[string]discoveryState)
	var stateMu sync.Mutex
	changed := make(chan struct{}, 1)
	deliveryDone := make(chan struct{})
	go func() {
		defer close(deliveryDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-ticker.C:
			}
			stateMu.Lock()
			snapshot := make(map[string]discoveryState, len(latest))
			for id, state := range latest {
				snapshot[id] = state
			}
			stateMu.Unlock()
			broadcastDiscovery(ctx, targets, snapshot)
		}
	}()
	defer func() { cancel(); <-deliveryDone }()
	for _, cluster := range cfg.Gateway.Clusters {
		if cluster.Discovery.Type == "" || cluster.Discovery.Type == "static" {
			continue
		}
		go discovery.Run(ctx, cluster.Discovery, func(result discovery.Result) {
			update := server.DiscoveryUpdate{Settings: cluster.Discovery, Endpoints: result.Endpoints, ObservedAt: result.ObservedAt, ResourceVersion: result.ResourceVersion}
			if result.Err != nil {
				update.Error = result.Err.Error()
			}
			select {
			case events <- discoveryDelivery{cluster.ID, update}:
			case <-ctx.Done():
			}
		})
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-events:
			stateMu.Lock()
			latest[event.cluster] = latest[event.cluster].observe(event.update)
			stateMu.Unlock()
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

func broadcastDiscovery(ctx context.Context, targets []*url.URL, states map[string]discoveryState) {
	var group sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for _, target := range targets {
		for cluster, state := range states {
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				group.Wait()
				return
			}
			group.Add(1)
			go func() {
				defer group.Done()
				defer func() { <-semaphore }()
				for _, update := range []*server.DiscoveryUpdate{state.Good, state.Failure} {
					if update == nil {
						continue
					}
					if err := deliverDiscovery(ctx, target, cluster, *update, os.Getenv("DISCOVERY_RESOLVE_TARGETS") != "false"); err != nil {
						slog.Warn("discovery delivery failed", "target", target.Host, "cluster", cluster, "error", err)
					}
				}
			}()
		}
	}
	group.Wait()
}

func deliverDiscovery(ctx context.Context, target *url.URL, cluster string, update server.DiscoveryUpdate, resolve bool) error {
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	addresses := []string{net.JoinHostPort(target.Hostname(), port)}
	if resolve {
		lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := net.DefaultResolver.LookupNetIP(lookup, "ip", target.Hostname())
		cancel()
		if err != nil {
			return err
		}
		addresses = nil
		for _, ip := range ips {
			addresses = append(addresses, net.JoinHostPort(ip.String(), port))
		}
		if len(addresses) == 0 {
			return fmt.Errorf("no replica addresses")
		}
	}
	payload, err := json.Marshal(update)
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	var failure error
	semaphore := make(chan struct{}, 8)
	for _, address := range addresses {
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			group.Wait()
			return ctx.Err()
		}
		group.Add(1)
		go func(address string) {
			defer group.Done()
			defer func() { <-semaphore }()
			if err := sendDiscovery(ctx, target, address, cluster, payload); err != nil {
				mu.Lock()
				failure = err
				mu.Unlock()
			}
		}(address)
	}
	group.Wait()
	return failure
}

func sendDiscovery(ctx context.Context, target *url.URL, address, cluster string, payload []byte) error {
	tlsConfig, err := identity.Client(config.ClientTLSConfig{Enabled: target.Scheme == "https", CAFile: os.Getenv("DISCOVERY_CA_FILE"), ServerName: target.Hostname(), ClientCertFile: os.Getenv("DISCOVERY_CLIENT_CERT_FILE"), ClientKeyFile: os.Getenv("DISCOVERY_CLIENT_KEY_FILE")})
	if err != nil {
		return err
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, ResponseHeaderTimeout: 25 * time.Second, TLSHandshakeTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	destination := *target
	destination.Path = "/api/v1/discovery/" + url.PathEscape(cluster)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, destination.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	token, err := config.SecretFromEnv("BALANCER_DISCOVERY_TOKEN")
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Balancer-CSRF", "1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("replica %s rejected discovery update: HTTP %d", address, response.StatusCode)
	}
	return nil
}
