/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resiliency

import (
	"context"
)

// NoOp is a true bypass implementation of `Provider`.
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

// ComponentInputPolicy returns a NoOp policy runner for a component input.
func (*NoOp) ComponentInputPolicy(ctx context.Context, name string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}

// ComponentOutputPolicy returns a NoOp policy runner for a component output.
func (*NoOp) ComponentOutputPolicy(ctx context.Context, name string) Runner {
	return func(oper Operation) error {
		return oper(ctx)
	}
}
