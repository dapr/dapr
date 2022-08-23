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

package pluggable

import (
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
)

// mapComponents maps from pluggablecomponent manifest to pluggable component obj.
func mapComponents(comps []componentsV1alpha1.PluggableComponent) []components.Pluggable {
	pluggableComponents := make([]components.Pluggable, len(comps))
	for idx, component := range comps {
		pluggableComponents[idx] = components.Pluggable{
			Name:    component.GetObjectMeta().GetName(),
			Type:    components.Type(component.Spec.Type),
			Version: component.Spec.Version,
		}
	}
	return pluggableComponents
}

// newFromPath creates a disk manifest loader from the given path.
func newFromPath(pluggableComponentsPath string) components.ManifestLoader[componentsV1alpha1.PluggableComponent] {
	return components.NewDiskManifestLoader(pluggableComponentsPath, func() componentsV1alpha1.PluggableComponent {
		var comp componentsV1alpha1.PluggableComponent
		comp.Spec = componentsV1alpha1.PluggableComponentSpec{}
		return comp
	})
}

// LoadFromDisk loads PluggableComponents from the given path.
func LoadFromDisk(pluggableComponentsPath string) ([]components.Pluggable, error) {
	comp, err := newFromPath(pluggableComponentsPath).Load()
	return mapComponents(comp), err
}

// TODO Load from K8S
// LoadFromKuberentes load pluggable components when running in a kubernetes mode.
func LoadFromKubernetes() ([]components.Pluggable, error) {
	return make([]components.Pluggable, 0), nil
}
