/*
Copyright 2022 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/assert"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

func TestDiskManifestLoaderDecodeValidYaml(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
   name: statestore
spec:
   type: state.couchbase
   metadata:
   - name: prop1
     value: value1
   - name: prop2
     value: value2
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
   name: pubsub
spec:
   type: pubsub.in-memory
   metadata:
   - name: rawPayload
     value: "true"
`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Empty(t, errs)
	assert.Len(t, components, 1)
	assert.Equal(t, "statestore", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value.String())
}

func TestDiskManifestLoaderDecodeInvalidComponent(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	yaml := `
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
   name: testsub
spec:
   metadata:
   - name: prop1
     value: value1
   - name: prop2
     value: value2
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
   name: state.couchbase
spec:
   metadata:
      badStructure: "So please ignore me"
`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Empty(t, components)
	assert.Len(t, errs, 1)
}

func TestDiskManifestLoaderDecodeUnsuspectingFile(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	components, errs := request.decodeYaml([]byte("hey there"))
	assert.Len(t, errs, 1)
	assert.Empty(t, components)
}

func TesSDiskManifesLoadertDecodeInvalidYaml(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: statestore`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Len(t, errs, 1)
	assert.Empty(t, components)
}

func TestDiskManifestLoaderDecodeValidMultiYaml(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
    name: statestore1
spec:
  type: state.couchbase
  metadata:
    - name: prop1
      value: value1
    - name: prop2
      value: value2
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
    name: statestore2
spec:
  type: state.redis
  metadata:
    - name: prop3
      value: value3
`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Empty(t, errs)
	assert.Len(t, components, 2)
	assert.Equal(t, "statestore1", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value.String())
	assert.Equal(t, "prop2", components[0].Spec.Metadata[1].Name)
	assert.Equal(t, "value2", components[0].Spec.Metadata[1].Value.String())

	assert.Equal(t, "statestore2", components[1].Name)
	assert.Equal(t, "state.redis", components[1].Spec.Type)
	assert.Len(t, components[1].Spec.Metadata, 1)
	assert.Equal(t, "prop3", components[1].Spec.Metadata[0].Name)
	assert.Equal(t, "value3", components[1].Spec.Metadata[0].Value.String())
}

func TestDiskManifestLoaderDecodeInValidDocInMultiYaml(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
    name: statestore1
spec:
  type: state.couchbase
  metadata:
    - name: prop1
      value: value1
    - name: prop2
      value: value2
---
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: invalidyaml
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
    name: statestore2
spec:
  type: state.redis
  metadata:
    - name: prop3
      value: value3
`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Len(t, errs, 1)
	assert.Len(t, components, 2)

	assert.Equal(t, "statestore1", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value.String())
	assert.Equal(t, "prop2", components[0].Spec.Metadata[1].Name)
	assert.Equal(t, "value2", components[0].Spec.Metadata[1].Value.String())

	assert.Equal(t, "statestore2", components[1].Name)
	assert.Equal(t, "state.redis", components[1].Spec.Type)
	assert.Len(t, components[1].Spec.Metadata, 1)
	assert.Equal(t, "prop3", components[1].Spec.Metadata[0].Name)
	assert.Equal(t, "value3", components[1].Spec.Metadata[0].Value.String())
}

func TestDiskManifestLoaderZeroValueFn(t *testing.T) {
	request := NewDiskManifestLoader[componentsV1alpha1.Component]("test_component_path")

	// Set the type "state.couchbase" as default value
	request.SetZeroValueFn(func() componentsV1alpha1.Component {
		return componentsV1alpha1.Component{
			Spec: componentsV1alpha1.ComponentSpec{
				Type: "state.couchbase",
			},
		}
	})

	yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
   name: statestore
spec:
   metadata:
    - name: prop1
      value: value1
    - name: prop2
      value: value2
`
	components, errs := request.decodeYaml([]byte(yaml))
	assert.Empty(t, errs)
	assert.Len(t, components, 1)
	assert.Equal(t, "statestore", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value.String())
}
