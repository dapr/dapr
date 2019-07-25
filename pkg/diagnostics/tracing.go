package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	routing "github.com/qiangxue/fasthttp-routing"
	"go.opencensus.io/trace"
)

const (
	CorrelationID = "correlation-id"
)

type Event struct {
	EventName   string        `json:"eventName,omitempty"`
	To          []string      `json:"to,omitempty"`
	Concurrency string        `json:"concurrency,omitempty"`
	CreatedAt   time.Time     `json:"createdAt,omitempty"`
	State       []KeyValState `json:"state,omitempty"`
	Data        interface{}   `json:"data,omitempty"`
}

type KeyValState struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

//SerializeSpanContext seralizes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

//DeserializeSpanContext deseralize a span cotnext from a string
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

func DeserializeSpanContextPointer(ctx string) *trace.SpanContext {
	if ctx == "" {
		return nil
	}
	var context *trace.SpanContext = &trace.SpanContext{}
	*context = DeserializeSpanContext(ctx)
	return context
}

func TraceSpanFromCorrelationId(corId string, operation string, actionMethod string, targetID string, from string, verbMethod string) (context.Context, *trace.Span) {
	var ctx context.Context
	var span *trace.Span
	if corId != "" {
		spanContext := DeserializeSpanContext(corId)
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

func TraceSpanFromContext(c context.Context, events *[]Event, operation string, includeEvent bool, includeEventBody bool) (context.Context, *trace.Span, *trace.SpanContext) {
	ctx, span := trace.StartSpan(c, operation)
	if includeEvent {
		AddEventAnnotations(events, span, includeEventBody)
	}
	var context *trace.SpanContext = &trace.SpanContext{}
	*context = span.SpanContext()
	return ctx, span, context
}
func TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string, includeEvent bool, includeEventBody bool) (context.Context, *trace.Span, *trace.SpanContext) {
	var ctx context.Context
	var span *trace.Span
	if c == nil {
		ctx, span = trace.StartSpan(context.Background(), operation)
	} else {
		corId := string(c.Request.Header.Peek(CorrelationID))
		if corId != "" {
			spanContext := DeserializeSpanContext(corId)
			ctx, span = trace.StartSpanWithRemoteParent(context.Background(), operation, spanContext)
		} else {
			ctx, span = trace.StartSpan(context.Background(), operation)
		}
	}
	if includeEvent {
		AddEventAnnotations(events, span, includeEventBody)
	}
	var context *trace.SpanContext
	if span != nil {
		context = &trace.SpanContext{}
		*context = span.SpanContext()
		return ctx, span, context
	} else {
		return ctx, span, nil
	}
}
func AddEventAnnotations(events *[]Event, span *trace.Span, includeEventBody bool) {
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
func SetSpanStatus(span *trace.Span, code int32, message string) {
	if span != nil {
		span.SetStatus(trace.Status{
			Code:    code,
			Message: message,
		})
	}
}
