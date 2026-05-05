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

package compstore

import wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"

func (c *ComponentStore) AddWorkflowAccessPolicy(policy wfaclapi.WorkflowAccessPolicy) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, p := range c.workflowAccessPolicies {
		if p.Name == policy.Name {
			c.workflowAccessPolicies[i] = policy
			return
		}
	}

	c.workflowAccessPolicies = append(c.workflowAccessPolicies, policy)
}

func (c *ComponentStore) ListWorkflowAccessPolicies() []wfaclapi.WorkflowAccessPolicy {
	c.lock.RLock()
	defer c.lock.RUnlock()

	policies := make([]wfaclapi.WorkflowAccessPolicy, len(c.workflowAccessPolicies))
	copy(policies, c.workflowAccessPolicies)

	return policies
}

func (c *ComponentStore) DeleteWorkflowAccessPolicy(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, p := range c.workflowAccessPolicies {
		if p.Name == name {
			c.workflowAccessPolicies = append(c.workflowAccessPolicies[:i], c.workflowAccessPolicies[i+1:]...)
			return
		}
	}
}
