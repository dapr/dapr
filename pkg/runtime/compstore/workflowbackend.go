/*
Copyright 2024 The Dapr Authors
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

import (
	"github.com/microsoft/durabletask-go/backend"
)

func (c *ComponentStore) AddWorkflowBackend(name string, backend backend.Backend) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.workflowBackends[name] = backend
}

func (c *ComponentStore) GetWorkflowBackend(name string) (backend.Backend, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	backend, ok := c.workflowBackends[name]
	return backend, ok
}

func (c *ComponentStore) ListWorkflowBackends() map[string]backend.Backend {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.workflowBackends
}

func (c *ComponentStore) DeleteWorkflowBackend(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.workflowBackends, name)
}
