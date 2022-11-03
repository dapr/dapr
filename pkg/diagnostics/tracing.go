/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package diagnostics

import (
	"context"
	"time"

	"github.com/pkg/errors"

	itrace "github.com/dapr/dapr/pkg/diagnostics/sdk/trace"
	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	apitrace "go.opentelemetry.io/otel/trace"
)

const (
	// daprInternalSpanAttrPrefix is the internal span attribution prefix.
	// Middleware will not populate it if the span key starts with this prefix.
	daprInternalSpanAttrPrefix = "__dapr."
	// daprAPISpanNameInternal is the internal attribution, but not populated
	// to span attribution.
	daprAPISpanNameInternalKey = attribute.Key(daprInternalSpanAttrPrefix + "spanname")

	daprRPCServiceInvocationService = "ServiceInvocation"
	daprRPCDaprService              = "Dapr"
	defaultTracingExporterAddr      = "localhost:4318"
	defaultMetricExporterAddr       = "localhost:4318"
)

var defaultTracer apitrace.Tracer = apitrace.
	NewNoopTracerProvider().Tracer("")

// StartInternalCallbackSpan starts trace span for internal callback such as input bindings and pubsub subscription.
func StartInternalCallbackSpan(ctx context.Context, spanName string, parent apitrace.SpanContext) (context.Context, apitrace.Span) {
	ctx = apitrace.ContextWithRemoteSpanContext(ctx, parent)
	return defaultTracer.Start(ctx, spanName,
		apitrace.WithSpanKind(apitrace.SpanKindServer))
}

type TracingClient struct {
	AppID     string
	Namespace string
	// Address collector receiver address.
	Address string
	Token   string

	sampleRatio float64
	exporter    *otlptrace.Exporter
	provider    *sdktrace.TracerProvider
}

// InitTracing init tracing client.
func InitTracing(address, token, appID, namespace string, sampleRatio float64) (*TracingClient, error) {
	if address == "" {
		address = defaultTracingExporterAddr
	}
	client := &TracingClient{
		Address:     address,
		Token:       token,
		AppID:       appID,
		Namespace:   namespace,
		sampleRatio: sampleRatio,
	}
	if err := client.init(); err != nil {
		return nil, err
	}

	return client, nil
}

func (t *TracingClient) init() error {
	var err error
	client := otlptracegrpc.NewClient(
		otlptracegrpc.WithEndpoint(t.Address),
		otlptracegrpc.WithInsecure(),
	)
	t.exporter, err = otlptrace.New(context.Background(), client)
	if err != nil {
		return errors.Errorf("creating OTLP trace exporter: %v", err)
	}

	ssp := sdktrace.NewBatchSpanProcessor(t.exporter,
		sdktrace.WithBatchTimeout(5*time.Second))
	t.provider = sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(ssp),
		sdktrace.WithSampler(itrace.TraceIDBasedParentAndRatio(t.sampleRatio)),
		sdktrace.WithResource(
			resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String(t.AppID),
				semconv.ServiceNamespaceKey.String(t.Namespace),
				attribute.String("token", t.Token),
			)),
	)
	otel.SetTracerProvider(t.provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	defaultTracer = otel.GetTracerProvider().Tracer(
		daprRPCDaprService,
		apitrace.WithInstrumentationVersion("v0.33.0"),
		apitrace.WithSchemaURL("https://dapr.io"),
	)

	return nil
}

// Close close tracing client.
func (t *TracingClient) Close() error {
	if t == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := t.provider.Shutdown(ctx); err != nil {
		return err
	}
	if err := t.exporter.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// AddAttributesToSpan adds the given attributes in the span.
func AddAttributesToSpan(span apitrace.Span, attrs []attribute.KeyValue) {
	if span == nil {
		return
	}
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// ConstructInputBindingSpanAttributes creates span attributes for InputBindings.
func ConstructInputBindingSpanAttributes(bindingName, url string) []attribute.KeyValue {
	return []attribute.KeyValue{
		isemconv.ComponentBindings,
		isemconv.ComponentNameKey.String(bindingName),
		isemconv.ComponentMethodKey.String(url),
	}
}

// ConstructSubscriptionSpanAttributes creates span attributes for Pubsub subscription.
func ConstructSubscriptionSpanAttributes(topic string) []attribute.KeyValue {
	return []attribute.KeyValue{
		isemconv.ComponentPubsub,
		isemconv.ComponentTopicKey.String(topic),
	}
}
