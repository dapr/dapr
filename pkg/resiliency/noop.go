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

// Compile-time check to ensure `*NoOp` satisfies the `Provider` interface.
var _ = (Provider)((*NoOp)(nil))

// RoutePolicy returns a NoOp policy runner for a route.
func (*NoOp) RoutePolicy(ctx context.Context, name string) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// EndpointPolicy returns a NoOp policy runner for a service endpoint.
func (*NoOp) EndpointPolicy(ctx context.Context, service string, endpoint string) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// ActorPreLockPolicy returns a NoOp policy runner for an actor instance.
func (*NoOp) ActorPreLockPolicy(ctx context.Context, actorType string, id string) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// ActorPostLockPolicy returns a NoOp policy runner for an actor instance.
func (*NoOp) ActorPostLockPolicy(ctx context.Context, actorType string, id string) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// ComponentInboundPolicy returns a NoOp inbound policy runner for a component.
func (*NoOp) ComponentInboundPolicy(ctx context.Context, name string, componentName ComponentType) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// ComponentOutboundPolicy returns a NoOp outbound policy runner for a component.
func (*NoOp) ComponentOutboundPolicy(ctx context.Context, name string, componentName ComponentType) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

// BuildInPolicy returns a NoOp policy runner for a built-in policy.
func (*NoOp) BuiltInPolicy(ctx context.Context, name BuiltInPolicyName) Runner {
	return func(oper Operation) (any, error) {
		return oper(ctx)
	}
}

func (*NoOp) GetPolicy(target string, policyType PolicyType) *PolicyDescription {
	return &PolicyDescription{}
}
