package tracing

import (
	"context"
	"os"

	"github.com/Azure/azure-amqp-common-go/internal"
	"go.opencensus.io/trace"
)

// StartSpanFromContext starts a span given a context and applies common library information
func StartSpanFromContext(ctx context.Context, operationName string, opts ...trace.StartOption) (*trace.Span, context.Context) {
	ctx, span := trace.StartSpan(ctx, operationName, opts...)
	ApplyComponentInfo(span)
	return span, ctx
}

// ApplyComponentInfo applies eventhub library and network info to the span
func ApplyComponentInfo(span *trace.Span) {
	span.AddAttributes(
		trace.StringAttribute("component", "github.com/Azure/azure-amqp-common-go"),
		trace.StringAttribute("version", common.Version))
	applyNetworkInfo(span)
}

func applyNetworkInfo(span *trace.Span) {
	hostname, err := os.Hostname()
	if err == nil {
		span.AddAttributes(trace.StringAttribute("peer.hostname", hostname))
	}
}
