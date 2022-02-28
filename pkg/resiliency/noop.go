package resiliency

import (
	"context"
)

// NoOp is a true bypass implementation of `Provider``.
type NoOp struct{}

// Ensure `*NoOp` satisfies the `Provider` interface.
var _ = (Provider)((*NoOp)(nil))

// RoutePolicy returns a NoOp policy runner for a route.
func (*NoOp) RoutePolicy(ctx context.Context, name string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}

// EndpointPolicy returns a NoOp policy runner for a service endpoint.
func (*NoOp) EndpointPolicy(ctx context.Context, service string, endpoint string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}

// ActorPolicy returns a NoOp policy runner for an actor instance.
func (*NoOp) ActorPolicy(ctx context.Context, actorType string, id string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}

// ComponentPolicy returns a NoOp policy runner for a component.
func (*NoOp) ComponentPolicy(ctx context.Context, name string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}
