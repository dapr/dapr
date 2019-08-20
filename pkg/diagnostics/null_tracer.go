package diagnostics

import (
	routing "github.com/qiangxue/fasthttp-routing"
)

// NullTracerSpan defines a tracing span for null tracer
type NullTracerSpan struct {
}

// End closes null tracing span
func (t NullTracerSpan) End() {
}

// SetStatus update OpenCensus tracing span status
func (t NullTracerSpan) SetStatus(code int32, msg string) {
}

// NullTracer is a null tracer that doesn't log anything
type NullTracer struct {
}

// TraceSpanFromRoutingContext creates a null tracing span from a routing context
func (o NullTracer) TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string) TracerSpan {
	return NullTracerSpan{}
}

// SetSpanStatus sets the status of a null tracing span
func (o NullTracer) SetSpanStatus(span TracerSpan, code int32, msg string) {
}

// SetSwitches update tracer verbosity switches
func (o NullTracer) SetSwitches(switches TracerSwitches) {
}
