/*
Copyright 2026 The Dapr Authors
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

package crossns

import (
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

// Option configures a cross-namespace Workflow fixture.
type Option func(*options)

type options struct {
	callerNS       string
	callerAppID    string
	targetNS       string
	targetAppID    string
	callerReplicas int
	targetReplicas int
	wfaclEnabled   bool
	policies       []*wfaclapi.WorkflowAccessPolicy
}

// WithCaller sets the caller app's (appID, namespace). Defaults to
// ("xns-caller", "default").
func WithCaller(appID, namespace string) Option {
	return func(c *options) {
		c.callerAppID = appID
		c.callerNS = namespace
	}
}

// WithTarget sets the target app's (appID, namespace). Defaults to
// ("xns-target", "other-ns").
func WithTarget(appID, namespace string) Option {
	return func(c *options) {
		c.targetAppID = appID
		c.targetNS = namespace
	}
}

// WithCallerReplicas sets the number of daprd replicas hosting the caller
// (appID, namespace) pair. Multi-replica setups exercise actor-host
// routing: name resolution picks one replica per cross-ns hop, and
// placement routes the reminder firing to whichever replica owns the
// actor. Defaults to 1.
func WithCallerReplicas(n int) Option {
	return func(c *options) { c.callerReplicas = n }
}

// WithTargetReplicas sets the number of daprd replicas hosting the target.
// See WithCallerReplicas for rationale. Defaults to 1.
func WithTargetReplicas(n int) Option {
	return func(c *options) { c.targetReplicas = n }
}

// WithWorkflowAccessPolicyEnabled toggles the WorkflowAccessPolicy
// feature flag on the shared Configuration. Defaults to false. Cross-
// namespace invocation is gated by this flag — disabling it effectively
// closes the cross-ns RPC surface, since the handler rejects any call
// not permitted by a loaded policy and no policies can load when the
// flag is off.
func WithWorkflowAccessPolicyEnabled(enabled bool) Option {
	return func(c *options) { c.wfaclEnabled = enabled }
}

// WithPolicies registers one or more WorkflowAccessPolicy resources to be
// loaded via the operator.
func WithPolicies(policies ...*wfaclapi.WorkflowAccessPolicy) Option {
	return func(c *options) { c.policies = append(c.policies, policies...) }
}
