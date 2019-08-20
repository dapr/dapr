package diagnostics

import (
	routing "github.com/qiangxue/fasthttp-routing"
)

// TracerSpan defines a tracing span that a tracer users to keep track of call scopes
type TracerSpan interface {
	End()
	SetStatus(code int32, msg string)
}

// TracerSwitches controls verbosity of the tracer
type TracerSwitches struct {
	IncludeEvent     bool
	IncludeEventBody bool
}

// Tracer defines the interface of a distributed tracer
type Tracer interface {
	TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string) TracerSpan
	SetSpanStatus(span TracerSpan, code int32, msg string)
	SetSwitches(switches TracerSwitches)
}
