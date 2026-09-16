package discovery

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

func TestEndpointSliceFiltersUnreadyTerminatingAndDuplicates(t *testing.T) {
	var slice endpointSlice
	if err := json.Unmarshal([]byte(`{"addressType":"IPv4","ports":[{"port":8080,"protocol":"TCP"}],"endpoints":[{"addresses":["10.0.0.1"],"conditions":{"ready":true}},{"addresses":["10.0.0.2"],"conditions":{"ready":false}},{"addresses":["10.0.0.3"],"conditions":{"ready":true,"terminating":true}},{"addresses":["10.0.0.1"]}]}`), &slice); err != nil {
		t.Fatal(err)
	}
	result := collectEndpoints(map[string]endpointSlice{"a": slice, "b": slice}, config.GatewayDiscoveryConfig{Port: 8080})
	if len(result) != 1 || result[0].URL != "http://10.0.0.1:8080" {
		t.Fatalf("unexpected endpoints: %+v", result)
	}
	if got := collectEndpoints(map[string]endpointSlice{}, config.GatewayDiscoveryConfig{}); got == nil || len(got) != 0 {
		t.Fatalf("empty authoritative list must remain empty: %+v", got)
	}
}

func TestKubernetesWatchAppliesEventsBookmarksAndAuthoritativeEmpty(t *testing.T) {
	client, closeServer := newTestKubeClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" || r.URL.Query().Get("resourceVersion") != "10" {
			t.Errorf("unexpected watch query: %s", r.URL.RawQuery)
		}
		_, _ = fmt.Fprintln(w, `{"type":"ADDED","object":{"metadata":{"name":"api-a","resourceVersion":"11"},"addressType":"IPv4","ports":[{"port":8080,"protocol":"TCP"}],"endpoints":[{"addresses":["10.0.0.1"],"conditions":{"ready":true}}]}}`)
		_, _ = fmt.Fprintln(w, `{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"12"}}}`)
		_, _ = fmt.Fprintln(w, `{"type":"DELETED","object":{"metadata":{"name":"api-a","resourceVersion":"13"}}}`)
	}))
	defer closeServer()
	var results []Result
	healthy, err := client.watchWithHealth(context.Background(), map[string]endpointSlice{}, "10", func(result Result) { results = append(results, result) })
	if err != nil {
		t.Fatal(err)
	}
	if !healthy {
		t.Fatal("watch with valid events must reset reconnect backoff")
	}
	if len(results) != 3 {
		t.Fatalf("expected event, bookmark and deletion, got %d", len(results))
	}
	if len(results[0].Endpoints) != 1 || results[0].ResourceVersion != "11" {
		t.Fatalf("bad added result: %+v", results[0])
	}
	if len(results[1].Endpoints) != 1 || results[1].ResourceVersion != "12" {
		t.Fatalf("bookmark must retain membership and advance resource version: %+v", results[1])
	}
	if results[2].Endpoints == nil || len(results[2].Endpoints) != 0 || results[2].ResourceVersion != "13" {
		t.Fatalf("deletion must publish an authoritative empty set: %+v", results[2])
	}
}

func TestKubernetesWatchErrorRequiresRelist(t *testing.T) {
	client, closeServer := newTestKubeClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"type":"ERROR","object":{"kind":"Status","code":410}}`)
	}))
	defer closeServer()
	emitted := 0
	healthy, err := client.watchWithHealth(context.Background(), map[string]endpointSlice{}, "10", func(Result) { emitted++ })
	if err == nil || !strings.Contains(err.Error(), "relist") {
		t.Fatalf("expected relist error, got %v", err)
	}
	if healthy {
		t.Fatal("immediate watch error must retain exponential backoff")
	}
	if emitted != 0 {
		t.Fatalf("error event must not publish membership, got %d updates", emitted)
	}
}

func TestKubernetesWatchCancellationInterruptsSilentStream(t *testing.T) {
	client, closeServer := newTestKubeClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer closeServer()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := client.watch(ctx, map[string]endpointSlice{}, "10", func(Result) {})
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("watch did not stop promptly after cancellation")
	}
}

func newTestKubeClient(t *testing.T, handler http.Handler) (*kubeClient, func()) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	token := filepath.Join(directory, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	client, err := newKubeClient(config.GatewayDiscoveryConfig{APIServer: upstream.URL, CAFile: ca, TokenFile: token, Namespace: "apps", Service: "api", Port: 8080})
	if err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	return client, func() {
		client.client.CloseIdleConnections()
		upstream.Close()
	}
}

func TestKubernetesListPaginationAndCredentialRotation(t *testing.T) {
	token := "first"
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong token")
		}
		if r.URL.Query().Get("labelSelector") != "kubernetes.io/service-name=api" {
			t.Error("missing service selector")
		}
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"10","continue":"next"},"items":[{"metadata":{"name":"a"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"10"},"items":[{"metadata":{"name":"b"}}]}`))
	}))
	defer upstream.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	secret := filepath.Join(directory, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := newKubeClient(config.GatewayDiscoveryConfig{APIServer: upstream.URL, CAFile: ca, TokenFile: secret, Namespace: "apps", Service: "api"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.client.CloseIdleConnections()
	for range 2 {
		slices, version, err := client.list(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(slices) != 2 || version != "10" {
			t.Fatalf("bad list: %v %s", slices, version)
		}
		token = "rotated"
		if err := os.WriteFile(secret, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatalf("expected four page reads, got %d", calls)
	}
}
