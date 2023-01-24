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

// NoOp is a true bypass implementation of `Provider`.
type NoOp struct{}

// RoutePolicy returns a NoOp policy definition for a route.
func (NoOp) RoutePolicy(name string) *PolicyDefinition {
	return nil
}

// EndpointPolicy returns a NoOp policy definition for a service endpoint.
func (NoOp) EndpointPolicy(service string, endpoint string) *PolicyDefinition {
	return nil
}

// ActorPreLockPolicy returns a NoOp policy definition for an actor instance.
func (NoOp) ActorPreLockPolicy(actorType string, id string) *PolicyDefinition {
	return nil
}

// ActorPostLockPolicy returns a NoOp policy definition for an actor instance.
func (NoOp) ActorPostLockPolicy(actorType string, id string) *PolicyDefinition {
	return nil
}

// ComponentInboundPolicy returns a NoOp inbound policy definition for a component.
func (NoOp) ComponentInboundPolicy(name string, componentName ComponentType) *PolicyDefinition {
	return nil
}

// ComponentOutboundPolicy returns a NoOp outbound policy definition for a component.
func (NoOp) ComponentOutboundPolicy(name string, componentName ComponentType) *PolicyDefinition {
	return nil
}

// BuildInPolicy returns a NoOp policy definition for a built-in policy.
func (NoOp) BuiltInPolicy(name BuiltInPolicyName) *PolicyDefinition {
	return nil
}

func (NoOp) PolicyDefined(target string, policyType PolicyType) bool {
	return true
}
