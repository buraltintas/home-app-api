package observability

import (
	"context"
	"errors"
	"fmt"

	"github.com/burakaltintas/home-app-api/internal/brand"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func SetupTracing(ctx context.Context, enabled bool, endpoint, environment string) (func(context.Context) error, error) {
	if !enabled {
		return func(context.Context) error { return nil }, nil
	}
	if endpoint == "" {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT is required when OTEL_ENABLED=true")
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes("", attribute.String("service.name", brand.ServiceName), attribute.String("deployment.environment", environment)))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func StartSpan(ctx context.Context, name string) (context.Context, func(error)) {
	ctx, span := otel.Tracer(brand.TracerName).Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient))
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			if !errors.Is(err, context.Canceled) {
				span.SetStatus(codes.Error, "operation failed")
			}
		}
		span.End()
	}
}
