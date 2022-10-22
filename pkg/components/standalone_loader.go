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

package components

import (
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	config "github.com/dapr/dapr/pkg/config/modes"
)

// StandaloneComponents loads components in a standalone mode environment.
type StandaloneComponents struct {
	componentsManifestLoader ManifestLoader[componentsV1alpha1.Component]
}

// newComponent creates a new component manifest.
func newComponent() componentsV1alpha1.Component {
	var comp componentsV1alpha1.Component
	comp.Spec = componentsV1alpha1.ComponentSpec{}
	return comp
}

// NewStandaloneComponents returns a new standalone loader.
func NewStandaloneComponents(configuration config.StandaloneConfig) *StandaloneComponents {
	return &StandaloneComponents{
		componentsManifestLoader: NewDiskManifestLoader(configuration.ComponentsPath, newComponent),
	}
}

// LoadComponents loads dapr components from a given directory.
func (s *StandaloneComponents) LoadComponents() ([]componentsV1alpha1.Component, error) {
	return s.componentsManifestLoader.Load()
}
