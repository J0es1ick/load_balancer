package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
)

type sliceMetadata struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion"`
	Continue        string `json:"continue"`
}
type endpointSlice struct {
	Metadata    sliceMetadata `json:"metadata"`
	AddressType string        `json:"addressType"`
	Ports       []struct {
		Port     *int    `json:"port"`
		Protocol *string `json:"protocol"`
	} `json:"ports"`
	Endpoints []struct {
		Addresses  []string `json:"addresses"`
		Conditions struct {
			Ready       *bool `json:"ready"`
			Serving     *bool `json:"serving"`
			Terminating *bool `json:"terminating"`
		} `json:"conditions"`
	} `json:"endpoints"`
}

type kubeClient struct {
	client    *http.Client
	base      string
	path      string
	tokenFile string
	settings  config.GatewayDiscoveryConfig
}

func newKubeClient(settings config.GatewayDiscoveryConfig) (*kubeClient, error) {
	base := settings.APIServer
	if base == "" {
		base = "https://kubernetes.default.svc"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return nil, fmt.Errorf("kubernetes API must use HTTPS without URL credentials")
	}
	caFile := settings.CAFile
	if caFile == "" {
		caFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	}
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("invalid Kubernetes CA")
	}
	token := settings.TokenFile
	if token == "" {
		token = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 10 * time.Second, IdleConnTimeout: time.Minute}
	return &kubeClient{client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: strings.TrimRight(base, "/"), path: "/apis/discovery.k8s.io/v1/namespaces/" + url.PathEscape(settings.Namespace) + "/endpointslices", tokenFile: token, settings: settings}, nil
}

func (client *kubeClient) request(ctx context.Context, query url.Values) (*http.Response, error) {
	query.Set("labelSelector", "kubernetes.io/service-name="+client.settings.Service)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.base+client.path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	token, err := os.ReadFile(client.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service account token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Accept", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("kubernetes API returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (client *kubeClient) list(ctx context.Context) (map[string]endpointSlice, string, error) {
	slices := make(map[string]endpointSlice)
	version := ""
	continuation := ""
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		query := url.Values{"limit": {"500"}}
		if continuation != "" {
			query.Set("continue", continuation)
		}
		response, err := client.request(requestCtx, query)
		if err != nil {
			cancel()
			return nil, "", err
		}
		var result struct {
			Metadata sliceMetadata   `json:"metadata"`
			Items    []endpointSlice `json:"items"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&result)
		response.Body.Close()
		cancel()
		if err != nil {
			return nil, "", err
		}
		for _, slice := range result.Items {
			slices[slice.Metadata.Name] = slice
		}
		version = result.Metadata.ResourceVersion
		continuation = result.Metadata.Continue
		if continuation == "" {
			return slices, version, nil
		}
	}
}

func runKubernetes(ctx context.Context, settings config.GatewayDiscoveryConfig, emit func(Result)) {
	backoff := time.Second
	for ctx.Err() == nil {
		client, err := newKubeClient(settings)
		if err != nil {
			emit(Result{Err: err, ObservedAt: time.Now().UTC()})
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		slices, version, err := client.list(ctx)
		if err == nil {
			emit(Result{Endpoints: collectEndpoints(slices, settings), ResourceVersion: version, ObservedAt: time.Now().UTC()})
			var healthy bool
			healthy, err = client.watchWithHealth(ctx, slices, version, emit)
			if healthy {
				backoff = time.Second
			}
		}
		client.client.CloseIdleConnections()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			emit(Result{Err: err, ObservedAt: time.Now().UTC()})
		}
		if !wait(ctx, backoff) {
			return
		}
		backoff = min(30*time.Second, backoff*2)
	}
}

func (client *kubeClient) watch(ctx context.Context, slices map[string]endpointSlice, version string, emit func(Result)) error {
	_, err := client.watchWithHealth(ctx, slices, version, emit)
	return err
}

func (client *kubeClient) watchWithHealth(ctx context.Context, slices map[string]endpointSlice, version string, emit func(Result)) (bool, error) {
	watchCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	response, err := client.request(watchCtx, url.Values{"watch": {"true"}, "allowWatchBookmarks": {"true"}, "resourceVersion": {version}, "timeoutSeconds": {"60"}})
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	started := time.Now()
	healthy := false
	decoder := json.NewDecoder(response.Body)
	for {
		var event struct {
			Type   string          `json:"type"`
			Object json.RawMessage `json:"object"`
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				return healthy || time.Since(started) >= 5*time.Second, nil
			}
			return healthy, err
		}
		if event.Type == "ERROR" {
			return healthy, fmt.Errorf("endpoint slice watch requires relist")
		}
		var slice endpointSlice
		if err := json.Unmarshal(event.Object, &slice); err != nil {
			return healthy, err
		}
		if slice.Metadata.ResourceVersion != "" {
			version = slice.Metadata.ResourceVersion
		}
		switch event.Type {
		case "ADDED", "MODIFIED":
			slices[slice.Metadata.Name] = slice
		case "DELETED":
			delete(slices, slice.Metadata.Name)
		case "BOOKMARK":
		default:
			continue
		}
		healthy = true
		emit(Result{Endpoints: collectEndpoints(slices, client.settings), ResourceVersion: version, ObservedAt: time.Now().UTC()})
	}
}

func collectEndpoints(slices map[string]endpointSlice, settings config.GatewayDiscoveryConfig) []config.GatewayEndpointConfig {
	result := []config.GatewayEndpointConfig{}
	seen := make(map[string]bool)
	scheme := settings.Scheme
	if scheme == "" {
		scheme = "http"
	}
	for _, slice := range slices {
		if slice.AddressType != "IPv4" && slice.AddressType != "IPv6" {
			continue
		}
		port := settings.Port
		if port < 1 || port > 65535 {
			continue
		}
		for _, endpoint := range slice.Endpoints {
			if (endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready) || (endpoint.Conditions.Serving != nil && !*endpoint.Conditions.Serving) || (endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating) {
				continue
			}
			for _, address := range endpoint.Addresses {
				if net.ParseIP(address) == nil {
					continue
				}
				host := net.JoinHostPort(address, strconv.Itoa(port))
				if seen[host] {
					continue
				}
				seen[host] = true
				result = append(result, config.GatewayEndpointConfig{ID: host, URL: scheme + "://" + host, Weight: 1})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
