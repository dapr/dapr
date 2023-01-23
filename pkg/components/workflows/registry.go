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

package workflows

import (
	"fmt"
	"strings"

	wfs "github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered state store implementations.
type Registry struct {
	Logger             logger.Logger
	workflowComponents map[string]func(logger.Logger) wfs.Workflow
}

// DefaultRegistry is the singleton with the registry .
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create workflow registry.
func NewRegistry() *Registry {
	return &Registry{
		workflowComponents: map[string]func(logger.Logger) wfs.Workflow{},
	}
}

// RegisterComponent adds a new workflow to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) wfs.Workflow, names ...string) {
	for _, name := range names {
		s.workflowComponents[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (wfs.Workflow, error) {
	if method, ok := s.getWorkflowComponent(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find wokflow %s/%s", name, version)
}

func (s *Registry) getWorkflowComponent(name, version, logName string) (func() wfs.Workflow, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	workflowFn, ok := s.workflowComponents[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(workflowFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		workflowFn, ok = s.workflowComponents[nameLower]
		if ok {
			return s.wrapFn(workflowFn, logName), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) wfs.Workflow, logName string) func() wfs.Workflow {
	return func() wfs.Workflow {
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
	return strings.ToLower("workflow." + name)
}
