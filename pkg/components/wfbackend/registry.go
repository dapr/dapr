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

package wfbackend

import (
	"fmt"
	"strings"

	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered workflow backend implementations.
type Registry struct {
	Logger                    logger.Logger
	workflowBackendComponents map[string]workflowBackendFactory
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create workflow registry.
func NewRegistry() *Registry {
	return &Registry{
		workflowBackendComponents: make(map[string]workflowBackendFactory),
	}
}

// RegisterComponent adds a new workflow to the registry.
func (s *Registry) RegisterComponent(componentFactory workflowBackendFactory, names ...string) {
	for _, name := range names {
		s.workflowBackendComponents[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (func(Metadata) (backend.Backend, error), error) {
	if method, ok := s.getWorkflowBackendComponent(name, version, logName); ok {
		return method, nil
	}
	return nil, fmt.Errorf("couldn't find wokflow backend %s/%s", name, version)
}

func (s *Registry) getWorkflowBackendComponent(name, version, logName string) (func(Metadata) (backend.Backend, error), bool) {
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

func (s *Registry) wrapFn(componentFactory workflowBackendFactory, logName string) func(Metadata) (backend.Backend, error) {
	return func(m Metadata) (backend.Backend, error) {
		l := s.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(m, l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("workflowbackend." + name)
}
