package components

import (
	"testing"

	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/stretchr/testify/assert"
)

func TestIsYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}

	assert.True(t, request.IsYaml("test.yaml"))
	assert.True(t, request.IsYaml("test.YAML"))
	assert.True(t, request.IsYaml("test.yml"))
	assert.True(t, request.IsYaml("test.YML"))
	assert.False(t, request.IsYaml("test.md"))
	assert.False(t, request.IsYaml("test.txt"))
	assert.False(t, request.IsYaml("test.sh"))
}

func TestStandaloneDecodeValidYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}
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
`
	components, errs := request.DecodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 1)
	assert.Empty(t, errs)
	assert.Equal(t, "statestore", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value)
}

func TestStandaloneDecodeInvalidYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}
	yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: statestore`
	components, errs := request.DecodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 0)
	assert.Len(t, errs, 1)
}

func TestStandaloneDecodeValidMultiYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}
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
	components, errs := request.DecodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 2)
	assert.Empty(t, errs)
	assert.Equal(t, "statestore1", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value)
	assert.Equal(t, "prop2", components[0].Spec.Metadata[1].Name)
	assert.Equal(t, "value2", components[0].Spec.Metadata[1].Value)

	assert.Equal(t, "statestore2", components[1].Name)
	assert.Equal(t, "state.redis", components[1].Spec.Type)
	assert.Len(t, components[1].Spec.Metadata, 1)
	assert.Equal(t, "prop3", components[1].Spec.Metadata[0].Name)
	assert.Equal(t, "value3", components[1].Spec.Metadata[0].Value)
}

func TestStandaloneDecodeInValidDocInMultiYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}
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
	components, errs := request.DecodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 2)
	assert.Len(t, errs, 1)

	assert.Equal(t, "statestore1", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value)
	assert.Equal(t, "prop2", components[0].Spec.Metadata[1].Name)
	assert.Equal(t, "value2", components[0].Spec.Metadata[1].Value)

	assert.Equal(t, "statestore2", components[1].Name)
	assert.Equal(t, "state.redis", components[1].Spec.Type)
	assert.Len(t, components[1].Spec.Metadata, 1)
	assert.Equal(t, "prop3", components[1].Spec.Metadata[0].Name)
	assert.Equal(t, "value3", components[1].Spec.Metadata[0].Value)
}
