package observability

import (
	"fmt"
	"io"
	"runtime"
	"time"
)

var Version = "dev"
var Commit = "unknown"
var BuildDate = "unknown"

type GatewayMetric struct {
	Revision      uint64
	Hash          string
	AppliedAt     time.Time
	ApplySuccess  uint64
	ApplyFailures uint64
	Clusters      []ClusterMetric
}

type ClusterMetric struct {
	ID                 string
	Requests           uint64
	Inflight           int64
	AvailableEndpoints int
	DiscoveryStale     bool
	LastUpdate         time.Time
}

func (metrics *Metrics) SetGatewayProvider(provider func() GatewayMetric) {
	metrics.providersMu.Lock()
	metrics.gatewayProvider = provider
	metrics.providersMu.Unlock()
}

func (metrics *Metrics) writeRuntimeMetrics(writer io.Writer, provider func() GatewayMetric) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	writeLine(writer, "# TYPE go_goroutines gauge")
	writeLine(writer, fmt.Sprintf("go_goroutines %d", runtime.NumGoroutine()))
	writeLine(writer, "# TYPE go_memstats_heap_alloc_bytes gauge")
	writeLine(writer, fmt.Sprintf("go_memstats_heap_alloc_bytes %d", memory.HeapAlloc))
	writeLine(writer, "# TYPE go_memstats_heap_objects gauge")
	writeLine(writer, fmt.Sprintf("go_memstats_heap_objects %d", memory.HeapObjects))
	writeLine(writer, "# TYPE go_gc_cycles_total counter")
	writeLine(writer, fmt.Sprintf("go_gc_cycles_total %d", memory.NumGC))
	writeLine(writer, "# TYPE process_start_time_seconds gauge")
	writeLine(writer, fmt.Sprintf("process_start_time_seconds %d", metrics.started.Unix()))
	writeLine(writer, "# TYPE load_balancer_build_info gauge")
	writeLine(writer, fmt.Sprintf("load_balancer_build_info{version=%q,commit=%q,go_version=%q} 1", Version, Commit, runtime.Version()))
	if provider == nil {
		return
	}
	state := provider()
	writeLine(writer, "# TYPE load_balancer_config_revision gauge")
	writeLine(writer, fmt.Sprintf("load_balancer_config_revision %d", state.Revision))
	writeLine(writer, "# TYPE load_balancer_config_last_applied_timestamp_seconds gauge")
	writeLine(writer, fmt.Sprintf("load_balancer_config_last_applied_timestamp_seconds %d", state.AppliedAt.Unix()))
	writeLine(writer, "# TYPE load_balancer_config_apply_total counter")
	writeLine(writer, fmt.Sprintf("load_balancer_config_apply_total{result=\"success\"} %d", state.ApplySuccess))
	writeLine(writer, fmt.Sprintf("load_balancer_config_apply_total{result=\"failure\"} %d", state.ApplyFailures))
	writeLine(writer, "# TYPE load_balancer_discovery_stale gauge")
	writeLine(writer, "# TYPE load_balancer_discovery_last_update_timestamp_seconds gauge")
	writeLine(writer, "# TYPE load_balancer_cluster_available_endpoints gauge")
	writeLine(writer, "# TYPE load_balancer_cluster_inflight_requests gauge")
	for _, cluster := range state.Clusters {
		writeLine(writer, fmt.Sprintf("load_balancer_cluster_available_endpoints{cluster=%q} %d", cluster.ID, cluster.AvailableEndpoints))
		writeLine(writer, fmt.Sprintf("load_balancer_cluster_inflight_requests{cluster=%q} %d", cluster.ID, cluster.Inflight))
		writeLine(writer, fmt.Sprintf("load_balancer_discovery_stale{cluster=%q} %d", cluster.ID, boolNumber(cluster.DiscoveryStale)))
		if !cluster.LastUpdate.IsZero() {
			writeLine(writer, fmt.Sprintf("load_balancer_discovery_last_update_timestamp_seconds{cluster=%q} %d", cluster.ID, cluster.LastUpdate.Unix()))
		}
	}
}
