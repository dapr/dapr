package diagnostics

import (
	routing "github.com/qiangxue/fasthttp-routing"
)

// ConsoleTracerSpan defines a tracing span for console tracer
type ConsoleTracerSpan struct {
}

// End closes console tracing span
func (t ConsoleTracerSpan) End() {
}

// SetStatus update console tracing span status
func (t ConsoleTracerSpan) SetStatus(code int32, msg string) {
}

// ConsoleTracer is a tracer that logs to console window
type ConsoleTracer struct {
}

// TraceSpanFromRoutingContext creates a console tracing span from a routing context
func (o ConsoleTracer) TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string) TracerSpan {
	return ConsoleTracerSpan{}
}

// SetSpanStatus sets the status of a console tracing span
func (o ConsoleTracer) SetSpanStatus(span TracerSpan, code int32, msg string) {
}

// SetSwitches update tracer verbosity switches
func (o ConsoleTracer) SetSwitches(switches TracerSwitches) {
}
