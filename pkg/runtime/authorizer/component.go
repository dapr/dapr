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

package authorizer

import (
	"strings"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

type componentDenyList struct {
	list []componentDenyListItem
}

func newComponentDenyList(raw []string) componentDenyList {
	list := make([]componentDenyListItem, len(raw))
	i := 0
	for _, comp := range raw {
		if comp == "" {
			continue
		}
		v := strings.Split(comp, "/")
		switch len(v) {
		case 1:
			list[i] = componentDenyListItem{
				typ: v[0],
			}
			i++
		case 2:
			list[i] = componentDenyListItem{
				typ:     v[0],
				version: v[1],
			}
			i++
		}
	}
	list = list[:i]
	return componentDenyList{list}
}

func (dl componentDenyList) IsAllowed(component componentsV1alpha1.Component) bool {
	if component.Spec.Type == "" || component.Spec.Version == "" {
		return false
	}
	for _, li := range dl.list {
		if li.typ == component.Spec.Type && (li.version == "" || li.version == component.Spec.Version) {
			log.Warnf("component '%s' cannot be loaded because components of type '%s' are not allowed", component.Name, component.LogName())
			return false
		}
	}
	return true
}

type componentDenyListItem struct {
	typ     string
	version string
}
