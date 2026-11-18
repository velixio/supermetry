// Generator for OpenTelemetry Go exporter acceptance: the stock OTLP/HTTP trace
// exporter pointed at an app project with only the endpoint and one header
// changed. Its own module so the SDK never enters the main go.mod.
// Usage: SENTRY_FIXTURE_DSN=http://<key>@127.0.0.1:<port>/<site> otel-go
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	u, err := url.Parse(os.Getenv("SENTRY_FIXTURE_DSN"))
	if err != nil || u.User == nil {
		fmt.Fprintln(os.Stderr, "SENTRY_FIXTURE_DSN must be a DSN")
		os.Exit(2)
	}
	key := u.User.Username()
	site := u.Path[1:]
	ctx := context.Background()
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(fmt.Sprintf("http://%s/api/%s/otlp/v1/traces", u.Host, site)),
		otlptracehttp.WithHeaders(map[string]string{"x-supermetry-key": key}))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res := resource.NewSchemaless(semconv.ServiceName("fixture-api"), semconv.ServiceVersion("1.0.0"), attribute.String("deployment.environment.name", "fixture"))
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	tr := tp.Tracer("fixture")

	ctx, root := tr.Start(ctx, "GET /api/items", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
		attribute.String("http.request.method", "GET"), attribute.String("http.route", "/api/items"),
		attribute.Int("http.response.status_code", 200), attribute.String("url.path", "/api/items")))
	_, db := tr.Start(ctx, "SELECT items", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system", "postgresql"), attribute.String("db.operation", "SELECT"),
		attribute.String("db.statement", "select * from items where owner = 'fixture' and id = 42")))
	time.Sleep(15 * time.Millisecond)
	db.End()
	_, call := tr.Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("http.request.method", "GET"), attribute.Int("http.response.status_code", 502),
		attribute.String("url.full", "https://api.example.com/v1/items/12345?token=fixture-token")))
	time.Sleep(10 * time.Millisecond)
	call.SetStatus(codes.Error, "bad gateway")
	call.End()
	root.End()
	if err := tp.Shutdown(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
