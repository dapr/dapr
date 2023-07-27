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

package meta

import (
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/modes"
)

type Options struct {
	ID        string
	PodName   string
	Namespace string
	Mode      modes.DaprMode
}

type Meta struct {
	id        string
	podName   string
	namespace string
	mode      modes.DaprMode
}

func New(options Options) *Meta {
	return &Meta{
		podName:   options.PodName,
		namespace: options.Namespace,
		id:        options.ID,
	}
}

func (m *Meta) ToBaseMetadata(comp compapi.Component) metadata.Base {
	return metadata.Base{
		Properties: m.convertItemsToProps(comp.Spec.Metadata),
		Name:       comp.Name,
	}
}

func (m *Meta) convertItemsToProps(items []common.NameValuePair) map[string]string {
	properties := map[string]string{}
	for _, c := range items {
		val := c.Value.String()
		for strings.Contains(val, "{uuid}") {
			val = strings.Replace(val, "{uuid}", uuid.New().String(), 1)
		}
		if strings.Contains(val, "{podName}") {
			if m.podName == "" {
				// TODO: @joshvanl: return error here rather than panicing.
				log.Fatalf("failed to parse metadata: property %s refers to {podName} but podName is not set", c.Name)
			}
			val = strings.ReplaceAll(val, "{podName}", m.podName)
		}
		val = strings.ReplaceAll(val, "{namespace}", fmt.Sprintf("%s.%s", m.namespace, m.id))
		val = strings.ReplaceAll(val, "{appID}", m.id)
		properties[c.Name] = val
	}
	return properties
}

func (m *Meta) AuthSecretStoreOrDefault(resource Resource) string {
	secretStore := resource.GetSecretStore()
	if secretStore == "" {
		switch m.mode {
		case modes.KubernetesMode:
			return "kubernetes"
		}
	}
	return secretStore
}

func ContainsNamespace(items []common.NameValuePair) bool {
	for _, c := range items {
		val := c.Value.String()
		if strings.Contains(val, "{namespace}") {
			return true
		}
	}
	return false
}
