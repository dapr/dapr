/*
Copyright 2023 The Dapr Authors
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

import wfbe "github.com/dapr/dapr/pkg/components/wfbackend"

func (c *ComponentStore) AddWorkflowBackend(name string, workflowBackend wfbe.WorkflowBackend) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.workflowBackends[name] = workflowBackend
}

func (c *ComponentStore) GetWorkflowBackend(name string) (wfbe.WorkflowBackend, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	workflowBackend, ok := c.workflowBackends[name]
	return workflowBackend, ok
}

func (c *ComponentStore) ListWorkflowBackends() map[string]wfbe.WorkflowBackend {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.workflowBackends
}

func (c *ComponentStore) DeleteWorkflowBackend(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.workflowBackends, name)
}
