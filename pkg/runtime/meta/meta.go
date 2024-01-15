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
	"strings"

	"github.com/google/uuid"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/modes"
)

const WasmStrictSandboxMetadataKey = "strictSandbox"

type Options struct {
	ID            string
	PodName       string
	Namespace     string
	StrictSandbox bool
	Mode          modes.DaprMode
}

type Meta struct {
	id            string
	podName       string
	namespace     string
	strictSandbox bool
	mode          modes.DaprMode
}

func New(options Options) *Meta {
	return &Meta{
		podName:       options.PodName,
		namespace:     options.Namespace,
		strictSandbox: options.StrictSandbox,
		id:            options.ID,
		mode:          options.Mode,
	}
}

func (m *Meta) ToBaseMetadata(comp compapi.Component) (metadata.Base, error) {
	// Add global wasm strict sandbox config to the wasm component metadata
	if components.IsWasmComponentType(comp.Spec.Type) {
		m.AddWasmStrictSandbox(&comp)
	}

	props, err := m.convertItemsToProps(comp.Spec.Metadata)
	if err != nil {
		return metadata.Base{}, err
	}

	return metadata.Base{
		Properties: props,
		Name:       comp.Name,
	}, nil
}

func (m *Meta) convertItemsToProps(items []common.NameValuePair) (map[string]string, error) {
	properties := map[string]string{}
	for _, c := range items {
		val := c.Value.String()
		for strings.Contains(val, "{uuid}") {
			u, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			val = strings.Replace(val, "{uuid}", u.String(), 1)
		}
		if strings.Contains(val, "{podName}") {
			if m.podName == "" {
				return nil, fmt.Errorf("failed to parse metadata: property %s refers to {podName} but podName is not set", c.Name)
			}
			val = strings.ReplaceAll(val, "{podName}", m.podName)
		}
		val = strings.ReplaceAll(val, "{namespace}", m.namespace+"."+m.id)
		val = strings.ReplaceAll(val, "{appID}", m.id)
		properties[c.Name] = val
	}
	return properties, nil
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

// AddWasmStrictSandbox adds global wasm strict sandbox configuration to component metadata.
// When strict sandbox is enabled, WASM components always run in strict mode regardless of their configuration.
// When strict sandbox is disabled or unset, keep the original component configuration.
func (m *Meta) AddWasmStrictSandbox(comp *compapi.Component) {
	// If the global strict sandbox is disabled (or unset), it is not enforced.
	if !m.strictSandbox {
		return
	}

	// If the metadata already contains the strict sandbox key, update the value to global strict sandbox config.
	for i, c := range comp.Spec.Metadata {
		if strings.EqualFold(c.Name, WasmStrictSandboxMetadataKey) {
			comp.Spec.Metadata[i].SetValue([]byte("true"))
			return
		}
	}

	// If the metadata does not contain the strict sandbox key, add it.
	sandbox := common.NameValuePair{
		Name: WasmStrictSandboxMetadataKey,
	}
	sandbox.SetValue([]byte("true"))
	comp.Spec.Metadata = append(comp.Spec.Metadata, sandbox)
}
