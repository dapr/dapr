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

import "github.com/dapr/components-contrib/workflows"

func (c *ComponentStore) AddWorkflow(name string, workflow workflows.Workflow) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.workflowComponents[name] = workflow
}

func (c *ComponentStore) GetWorkflow(name string) (workflows.Workflow, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	workflow, ok := c.workflowComponents[name]
	return workflow, ok
}

func (c *ComponentStore) ListWorkflows() map[string]workflows.Workflow {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.workflowComponents
}

func (c *ComponentStore) DeleteWorkflow(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.workflowComponents, name)
}
