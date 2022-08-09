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
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/components"
)

type Workflow struct {
	Names         []string
	FactoryMethod func() workflows.Workflow
}

// New creates a new Workflow.
func New(name string, factoryMethod func() workflows.Workflow, aliases ...string) Workflow {
	names := []string{name}
	if len(aliases) > 0 {
		names = append(names, aliases...)
	}
	return Workflow{
		Names:         names,
		FactoryMethod: factoryMethod,
	}
}

// Registry is an interface for a component that returns registered workflow implementations.
type Registry interface {
	Register(components ...Workflow)
	Create(name, version string) (workflows.Workflow, error)
}

type workflowRegistry struct {
	workflows map[string]func() workflows.Workflow
}

// NewRegistry is used to create workflow registry.
func NewRegistry() Registry {
	return &workflowRegistry{
		workflows: map[string]func() workflows.Workflow{},
	}
}

// // Register registers a new factory method that creates an instance of a Workflow.
// // The key is the name of the workflow
func (s *workflowRegistry) Register(components ...Workflow) {
	for _, component := range components {
		for _, name := range component.Names {
			s.workflows[createFullName(name)] = component.FactoryMethod
		}
	}
}

func (s *workflowRegistry) Create(name, version string) (workflows.Workflow, error) {
	if method, ok := s.getWorkflow(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find wokflow %s/%s", name, version)
}

func (s *workflowRegistry) getWorkflow(name, version string) (func() workflows.Workflow, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	workflowFn, ok := s.workflows[nameLower+"/"+versionLower]
	if ok {
		return workflowFn, true
	}
	if components.IsInitialVersion(versionLower) {
		workflowFn, ok = s.workflows[nameLower]
	}
	return workflowFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("workflow." + name)
}
