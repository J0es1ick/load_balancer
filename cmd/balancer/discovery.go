package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/discovery"
	"github.com/J0es1ick/cloud_test_assignment/internal/gateway"
)

func runLocalDiscovery(ctx context.Context, engine *gateway.Engine) {
	type worker struct {
		key    string
		cancel context.CancelFunc
	}
	workers := map[string]worker{}
	var gate sync.Mutex
	defer func() {
		for _, value := range workers {
			value.cancel()
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		wanted := map[string]bool{}
		for _, cluster := range engine.Config().Clusters {
			if cluster.Discovery.Type == "static" {
				continue
			}
			wanted[cluster.ID] = true
			data, _ := json.Marshal(cluster.Discovery)
			key := string(data)
			if old, ok := workers[cluster.ID]; ok {
				if old.key == key {
					continue
				}
				old.cancel()
			}
			workerCtx, cancel := context.WithCancel(ctx)
			workers[cluster.ID] = worker{key, cancel}
			go discovery.Run(workerCtx, cluster.Discovery, func(result discovery.Result) {
				gate.Lock()
				defer gate.Unlock()
				if workerCtx.Err() != nil {
					return
				}
				current := findDiscovery(engine.Config(), cluster.ID)
				encoded, _ := json.Marshal(current)
				if string(encoded) != key {
					return
				}
				if result.Err != nil {
					_ = engine.MarkDiscoveryErrorForSource(cluster.ID, result.Err, true, cluster.Discovery)
					return
				}
				if err := engine.UpdateEndpointsForSource(workerCtx, cluster.ID, result.Endpoints, gateway.DiscoveryStatus{Type: cluster.Discovery.Type, LastUpdate: result.ObservedAt}, cluster.Discovery); err != nil {
					slog.Warn("discovery update rejected", "cluster", cluster.ID, "error", err)
				}
			})
		}
		for id, value := range workers {
			if !wanted[id] {
				value.cancel()
				delete(workers, id)
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func findDiscovery(value *config.GatewayConfig, id string) config.GatewayDiscoveryConfig {
	if value != nil {
		for _, cluster := range value.Clusters {
			if cluster.ID == id {
				return cluster.Discovery
			}
		}
	}
	return config.GatewayDiscoveryConfig{}
}
