package observability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func ConfigureTracing(ctx context.Context, settings config.TelemetryConfig) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if settings.OTLPEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	options := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(settings.OTLPEndpoint), otlptracehttp.WithTimeout(3 * time.Second)}
	if settings.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	name := settings.ServiceName
	if name == "" {
		name = "go-l7-proxy"
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(2048), sdktrace.WithMaxExportBatchSize(256)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(settings.SampleRate))),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", name))),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func TraceRequest(request *http.Request) (*http.Request, trace.Span) {
	ctx := otel.GetTextMapPropagator().Extract(request.Context(), propagation.HeaderCarrier(request.Header))
	ctx, span := otel.Tracer("go-l7-proxy").Start(ctx, "proxy.request", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.request.method", request.Method)))
	return request.WithContext(ctx), span
}

func TraceID(ctx context.Context) string {
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return ""
	}
	return span.TraceID().String()
}

type tracingTransport struct{ base http.RoundTripper }

func TraceTransport(base http.RoundTripper) http.RoundTripper { return tracingTransport{base: base} }
func (transport tracingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, span := otel.Tracer("go-l7-proxy").Start(request.Context(), "proxy.upstream", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("http.request.method", request.Method), attribute.String("server.address", request.URL.Hostname())))
	out := request.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(out.Header))
	response, err := transport.base.RoundTrip(out)
	if err != nil {
		span.SetStatus(codes.Error, "upstream transport failed")
		span.End()
		return response, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode >= 500 {
		span.SetStatus(codes.Error, "upstream server error")
	}
	response.Body = &tracedBody{ReadCloser: response.Body, span: span}
	return response, nil
}

type tracedBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
}

func (body *tracedBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(func() { body.span.End() })
	return err
}
func (body *tracedBody) Write(value []byte) (int, error) {
	writer, ok := body.ReadCloser.(io.Writer)
	if !ok {
		return 0, fmt.Errorf("response is not an upgraded connection")
	}
	return writer.Write(value)
}
