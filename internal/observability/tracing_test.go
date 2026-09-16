package observability

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type traceTransportFunc func(*http.Request) (*http.Response, error)

func (function traceTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestTracingPropagatesButDoesNotExportSensitiveURLs(t *testing.T) {
	previous := otel.GetTracerProvider()
	propagator := otel.GetTextMapPropagator()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
	}()
	request, err := http.NewRequest("GET", "https://example.test/users/sensitive?token=secret", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer secret")
	request, span := TraceRequest(request)
	transport := TraceTransport(traceTransportFunc(func(out *http.Request) (*http.Response, error) {
		assert.NotEmpty(t, out.Header.Get("Traceparent"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	}))
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	span.End()
	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID())
	for _, finished := range spans {
		for _, attribute := range finished.Attributes {
			assert.NotContains(t, attribute.Value.AsString(), "secret")
			assert.NotContains(t, attribute.Value.AsString(), "sensitive")
		}
	}
}
