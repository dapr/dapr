package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/actionscore/actions/pkg/config"
	"go.opencensus.io/trace"
)

const (
	// CorrelationID is an opencensus corellation id
	CorrelationID = "correlation-id"
	// StatusCodeInternal indicates a server-side error
	StatusCodeInternal = 500
	// StatusCodeOK indicates a successful operation
	StatusCodeOK = 200
)

// Event is an Actions event
type Event struct {
	EventName   string        `json:"eventName,omitempty"`
	To          []string      `json:"to,omitempty"`
	Concurrency string        `json:"concurrency,omitempty"`
	CreatedAt   time.Time     `json:"createdAt,omitempty"`
	State       []KeyValState `json:"state,omitempty"`
	Data        interface{}   `json:"data,omitempty"`
}

// KeyValState is a state key value state
type KeyValState struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

//SerializeSpanContext seralizes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

//DeserializeSpanContext deseralizes a span cotnext from a string
func DeserializeSpanContext(ctx string) trace.SpanContext {
	parts := strings.Split(ctx, ";")
	spanID, _ := hex.DecodeString(parts[0])
	traceID, _ := hex.DecodeString(parts[1])
	traceOptions, _ := strconv.ParseUint(parts[2], 10, 32)
	ret := trace.SpanContext{}
	copy(ret.SpanID[:], spanID[:])
	copy(ret.TraceID[:], traceID[:])
	ret.TraceOptions = trace.TraceOptions(traceOptions)
	return ret
}

// DeserializeSpanContextPointer deseralizes a span context from a trace pointer
func DeserializeSpanContextPointer(ctx string) *trace.SpanContext {
	if ctx == "" {
		return nil
	}
	var context *trace.SpanContext = &trace.SpanContext{}
	*context = DeserializeSpanContext(ctx)
	return context
}

// TraceSpanFromCorrelationId traces a span from a given correlation id
func TraceSpanFromCorrelationId(corID string, operation string, actionMethod string, targetID string, from string, verbMethod string) (context.Context, *trace.Span) {
	var ctx context.Context
	var span *trace.Span
	if corID != "" {
		spanContext := DeserializeSpanContext(corID)
		ctx, span = trace.StartSpanWithRemoteParent(context.Background(), operation, spanContext)
	} else {
		ctx, span = trace.StartSpan(context.Background(), operation)
	}
	attrs := []trace.Attribute{
		trace.StringAttribute("actionMethod", actionMethod),
		trace.StringAttribute("targetID", targetID),
		trace.StringAttribute("from", from),
		trace.StringAttribute("verbMethod", verbMethod),
	}
	span.Annotate(attrs, "actionCall")
	span.AddAttributes(attrs...)
	return ctx, span
}

//// TraceSpanFromContext starts a span and traces a context with the given params
//func TraceSpanFromContext(c context.Context, events *[]Event, operation string, includeEvent bool, includeEventBody bool) (context.Context, *trace.Span, *trace.SpanContext) {
//	ctx, span := trace.StartSpan(c, operation)
//	if includeEvent {
//		addEventAnnotations(events, span, includeEventBody)
//	}
//	var context *trace.SpanContext = &trace.SpanContext{}
//	*context = span.SpanContext()
//	return ctx, span, context
//}

// CreateTracer creates a new tracer instance based on the given spec
func CreateTracer(action_id string, action_address string, spec config.TracingSpec, buffer *string) Tracer {
	var tracer Tracer
	switch t := spec.TracerType; t {
	case config.OpenCensusTracer:
		tracer = OCTracer{}
	default:
		tracer = NullTracer{}
	}
	tracer.Init(action_id, action_address, spec, buffer)
	return tracer
}
