/*
Copyright 2026 The Dapr Authors
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
	"strings"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

const (
	// DaprComponentNameSpanAttributeKey is the name of the Dapr component the
	// span calls into, e.g. "statestore".
	DaprComponentNameSpanAttributeKey = "dapr.component.name"
	// DaprComponentTypeSpanAttributeKey is the full Dapr component type, e.g.
	// "state.mongodb".
	DaprComponentTypeSpanAttributeKey = "dapr.component.type"

	// Component operations that have no metric counterpart in
	// component_monitoring.go.
	BulkSet          = "bulk_set"
	KeysLike         = "keys_like"
	DeleteWithPrefix = "delete_with_prefix"
)

// ComponentSpanInfo identifies the component a span is emitted for.
type ComponentSpanInfo struct {
	// Name is the component name from the Component resource.
	Name string
	// Type is the component type from the Component resource, e.g.
	// "state.mongodb".
	Type string
}

// Attributes returns the attributes common to every span for this component.
func (c ComponentSpanInfo) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.DBSystemNameKey.String(dbSystemName(c.Type)),
		attribute.String(DaprComponentNameSpanAttributeKey, c.Name),
		attribute.String(DaprComponentTypeSpanAttributeKey, c.Type),
	}
}

// StartComponentSpan starts a client span named "<operation> <component name>"
// for a call from daprd into a component. It returns a nil span when ctx has no
// sampled span, so callers outside a trace do not start new root spans.
func StartComponentSpan(ctx context.Context, info ComponentSpanInfo, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	// The HTTP API middleware stores its span under a Dapr context key, not the
	// OpenTelemetry one, so re-attach it or HTTP API calls start a new trace.
	parent := diagUtils.SpanFromContext(ctx)
	if !parent.SpanContext().IsSampled() {
		return ctx, nil
	}
	ctx = trace.ContextWithSpan(ctx, parent)

	all := make([]attribute.KeyValue, 0, 4+len(attrs))
	all = append(all, info.Attributes()...)
	all = append(all, semconv.DBOperationNameKey.String(operation))
	all = append(all, attrs...)

	//nolint:spancheck
	return tracer.Start(ctx, operation+" "+info.Name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(all...),
	)
}

// EndComponentSpan records err on span, if any, and ends it. It is a no-op
// for a nil span.
func EndComponentSpan(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.SetAttributes(semconv.ErrorType(err))
		span.SetStatus(otelcodes.Error, err.Error())
	}
	span.End()
}

// dbSystemName drops the building block prefix from a component type, e.g.
// "state.azure.cosmosdb" -> "azure.cosmosdb".
func dbSystemName(componentType string) string {
	_, name, found := strings.Cut(componentType, ".")
	if !found {
		return componentType
	}
	return name
}
