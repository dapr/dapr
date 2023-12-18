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

package workflowBackend

import (
	"fmt"
	"strings"

	wfbe "github.com/dapr/components-contrib/wfbackend"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered state store implementations.
type Registry struct {
	Logger                    logger.Logger
	workflowBackendComponents map[string]func(logger.Logger) wfbe.WorkflowBackend
}

// DefaultRegistry is the singleton with the registry .
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create workflow registry.
func NewRegistry() *Registry {
	return &Registry{
		workflowBackendComponents: make(map[string]func(logger.Logger) wfbe.WorkflowBackend),
	}
}

// RegisterComponent adds a new workflow to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) wfbe.WorkflowBackend, names ...string) {
	for _, name := range names {
		s.workflowBackendComponents[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (wfbe.WorkflowBackend, error) {
	if method, ok := s.getWorkflowBackendComponent(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find wokflow backend %s/%s", name, version)
}

func (s *Registry) getWorkflowBackendComponent(name, version, logName string) (func() wfbe.WorkflowBackend, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	workflowFn, ok := s.workflowBackendComponents[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(workflowFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		workflowFn, ok = s.workflowBackendComponents[nameLower]
		if ok {
			return s.wrapFn(workflowFn, logName), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) wfbe.WorkflowBackend, logName string) func() wfbe.WorkflowBackend {
	return func() wfbe.WorkflowBackend {
		l := s.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("workflowbackend." + name)
}
