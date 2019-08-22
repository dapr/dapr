package diagnostics

import (
	"github.com/actionscore/actions/pkg/config"
	routing "github.com/qiangxue/fasthttp-routing"
)

// TracerSpan defines a tracing span that a tracer users to keep track of call scopes
type TracerSpan interface {
	End()
	SetStatus(code int32, msg string)
}

// Tracer defines the interface of a distributed tracer
type Tracer interface {
	TraceSpanFromRoutingContext(c *routing.Context, events *[]Event, operation string) TracerSpan
	SetSpanStatus(span TracerSpan, code int32, msg string)
	Init(action_id string, action_address string, spec config.TracingSpec, buffer *string)
}
