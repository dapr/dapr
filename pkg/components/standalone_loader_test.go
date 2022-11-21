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
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	config "github.com/dapr/dapr/pkg/config/modes"

	"github.com/stretchr/testify/assert"
)

const configPrefix = "."

func writeTempConfig(path, content string) (delete func(), error error) {
	filePath := filepath.Join(configPrefix, path)
	err := os.WriteFile(filePath, []byte(content), fs.FileMode(0o644))
	return func() {
		os.Remove(filePath)
	}, err
}

func TestLoadComponentsFromFile(t *testing.T) {
	t.Run("valid yaml content", func(t *testing.T) {
		request := NewStandaloneComponents(config.StandaloneConfig{
			ComponentsPath: configPrefix,
		})
		filename := "test-component-valid.yaml"
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
		remove, err := writeTempConfig(filename, yaml)
		assert.Nil(t, err)
		defer remove()
		components, err := request.LoadComponents()
		assert.Nil(t, err)
		assert.Len(t, components, 1)
	})

	t.Run("invalid yaml head", func(t *testing.T) {
		request := NewStandaloneComponents(config.StandaloneConfig{
			ComponentsPath: configPrefix,
		})

		filename := "test-component-invalid.yaml"
		yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: statestore`
		remove, err := writeTempConfig(filename, yaml)
		assert.Nil(t, err)
		defer remove()
		components, err := request.LoadComponents()
		assert.Nil(t, err)
		assert.Len(t, components, 0)
	})

	t.Run("load components file not exist", func(t *testing.T) {
		request := NewStandaloneComponents(config.StandaloneConfig{
			ComponentsPath: "test-path-no-exists",
		})

		components, err := request.LoadComponents()
		assert.NotNil(t, err)
		assert.Len(t, components, 0)
	})
}
