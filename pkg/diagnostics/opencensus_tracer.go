package diagnostics

import (
	"context"
	"fmt"
	"strings"

	routing "github.com/qiangxue/fasthttp-routing"
	"go.opencensus.io/trace"
)

// OCTracerSpan defines a tracing span for OpenCensus tracer
type OCTracerSpan struct {
	Context     context.Context
	Span        *trace.Span
	SpanContext *trace.SpanContext
}

// End closes OpenCensus tracing span
func (t OCTracerSpan) End() {
	if t.Span != nil {
		t.Span.End()
	}
}

// SetStatus update OpenCensus tracing span status
func (t OCTracerSpan) SetStatus(code int32, msg string) {
	if t.Span != nil {
		t.Span.SetStatus(trace.Status{
			Code:    code,
			Message: msg,
		})
	}
}

// OCTracer is the OpenCensus tracer implementation
type OCTracer struct {
	Switches TracerSwitches
}

// TraceSpanFromRoutingContext creates a OpenCensus tracing span from a routing context
func (o OCTracer) TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string) TracerSpan {
	var ctx context.Context
	var span *trace.Span
	if c == nil {
		ctx, span = trace.StartSpan(context.Background(), operation)
	} else {
		corID := string(c.Request.Header.Peek(CorrelationID))
		if corID != "" {
			spanContext := DeserializeSpanContext(corID)
			ctx, span = trace.StartSpanWithRemoteParent(context.Background(), operation, spanContext)
		} else {
			ctx, span = trace.StartSpan(context.Background(), operation)
		}
	}
	if o.Switches.IncludeEvent {
		o.addEventAnnotations(events, span, o.Switches.IncludeEventBody)
	}
	var context *trace.SpanContext
	if span != nil {
		context = &trace.SpanContext{}
		*context = span.SpanContext()
		return OCTracerSpan{Context: ctx, Span: span, SpanContext: context}
	}
	return OCTracerSpan{Context: ctx, Span: span, SpanContext: nil}
}

// SetSpanStatus sets the status of a OpenCensus tracing span
func (o OCTracer) SetSpanStatus(span TracerSpan, code int32, msg string) {
	span.SetStatus(code, msg)
}

// SetSwitches update tracer verbosity switches
func (o OCTracer) SetSwitches(switches TracerSwitches) {
	o.Switches.IncludeEventBody = switches.IncludeEventBody
	o.Switches.IncludeEvent = switches.IncludeEvent
}

func (o *OCTracer) addEventAnnotations(events *[]Event, span *trace.Span, includeEventBody bool) {
	for _, e := range *events {
		attrs := []trace.Attribute{
			trace.StringAttribute("eventName", e.EventName),
			trace.StringAttribute("createdAt", e.CreatedAt.String()),
			trace.StringAttribute("concurrency", e.Concurrency),
			trace.StringAttribute("to", strings.Join(e.To, ",")),
		}
		span.Annotate(attrs, "message")
		if includeEventBody {
			attrs = append(attrs, trace.StringAttribute("data", fmt.Sprintf("%v", e.Data)))
		}
		span.AddAttributes(attrs...)
	}
}
