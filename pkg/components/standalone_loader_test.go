package components

import (
	"testing"

	"github.com/stretchr/testify/assert"

	config "github.com/dapr/dapr/pkg/config/modes"
)

func TestIsYaml(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}

	assert.True(t, request.isYaml("test.yaml"))
	assert.True(t, request.isYaml("test.YAML"))
	assert.True(t, request.isYaml("test.yml"))
	assert.True(t, request.isYaml("test.YML"))
	assert.False(t, request.isYaml("test.md"))
	assert.False(t, request.isYaml("test.txt"))
	assert.False(t, request.isYaml("test.sh"))
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
	components, errs := request.decodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 1)
	assert.Empty(t, errs)
	assert.Equal(t, "statestore", components[0].Name)
	assert.Equal(t, "state.couchbase", components[0].Spec.Type)
	assert.Len(t, components[0].Spec.Metadata, 2)
	assert.Equal(t, "prop1", components[0].Spec.Metadata[0].Name)
	assert.Equal(t, "value1", components[0].Spec.Metadata[0].Value.String())
}

func TestStandaloneDecodeInvalidComponent(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}
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
`
	components, errs := request.decodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 0)
	assert.Len(t, errs, 0)
}

func TestStandaloneDecodeUnsuspectingFile(t *testing.T) {
	request := &StandaloneComponents{
		config: config.StandaloneConfig{
			ComponentsPath: "test_component_path",
		},
	}

	components, errs := request.decodeYaml("components/messagebus.yaml", []byte("hey there"))
	assert.Len(t, components, 0)
	assert.Len(t, errs, 0)
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
	components, errs := request.decodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 0)
	assert.Len(t, errs, 0)
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
	components, errs := request.decodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 2)
	assert.Empty(t, errs)
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
	components, errs := request.decodeYaml("components/messagebus.yaml", []byte(yaml))
	assert.Len(t, components, 2)
	assert.Len(t, errs, 0)

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
