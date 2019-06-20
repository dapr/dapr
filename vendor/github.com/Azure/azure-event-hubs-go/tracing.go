package eventhub

import (
	"context"
	"net/http"
	"os"
	"strconv"

	"go.opencensus.io/trace"
)

func (h *Hub) startSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	return span, ctx
}

func (ns *namespace) startSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	return span, ctx
}

func (s *sender) startProducerSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	span.AddAttributes(
		trace.StringAttribute("span.kind", "producer"),
		trace.StringAttribute("message_bus.destination", s.getFullIdentifier()),
	)
	return span, ctx
}

func (r *receiver) startConsumerSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	span.AddAttributes(
		trace.StringAttribute("span.kind", "consumer"),
		trace.StringAttribute("message_bus.destination", r.getFullIdentifier()),
	)
	return span, ctx
}

func (r *receiver) startConsumerSpanFromWire(ctx context.Context, operationName string, reference trace.SpanContext, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpanWithRemoteParent(ctx, operationName, reference, opts...)
	ApplyComponentInfo(span)
	span.AddAttributes(
		trace.StringAttribute("span.kind", "consumer"),
		trace.StringAttribute("message_bus.destination", r.getFullIdentifier()),
	)
	return span, ctx
}

func (em *entityManager) startSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	span.AddAttributes(trace.StringAttribute("span.kind", "client"))
	return span, ctx
}

// ApplyComponentInfo applies eventhub library and network info to the span
func ApplyComponentInfo(span *trace.Span) {
	span.AddAttributes(
		trace.StringAttribute("component", "github.com/Azure/azure-event-hubs-go"),
		trace.StringAttribute("version", Version))
	applyNetworkInfo(span)
}

func applyNetworkInfo(span *trace.Span) {
	hostname, err := os.Hostname()
	if err == nil {
		span.AddAttributes(trace.StringAttribute("peer.hostname", hostname))
	}
}

func applyRequestInfo(span *trace.Span, req *http.Request) {
	span.AddAttributes(
		trace.StringAttribute("http.url", req.URL.String()),
		trace.StringAttribute("http.method", req.Method),
	)
}

func applyResponseInfo(span *trace.Span, res *http.Response) {
	if res != nil {
		span.AddAttributes(trace.StringAttribute("http.status_code", strconv.Itoa(res.StatusCode)))
	}
}
