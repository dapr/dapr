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
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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

func TestLoadPluggableComponentsFromFile(t *testing.T) {
	t.Run("valid pluggable component yaml content", func(t *testing.T) {
		filename := "test-pluggable-component-valid.yaml"
		yaml := `
apiVersion: dapr.io/v1alpha1
kind: PluggableComponent
metadata:
  name: mystate
spec:
  type: state
  version: v1
`
		remove, err := writeTempConfig(filename, yaml)
		assert.Nil(t, err)
		defer remove()
		components, err := LoadFromDisk(configPrefix)
		assert.Nil(t, err)
		assert.Len(t, components, 1)
	})

	t.Run("invalid yaml head", func(t *testing.T) {
		filename := "test-pluggable-component-invalid.yaml"
		yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: PluggableComponent
metadata:
name: statestore`
		remove, err := writeTempConfig(filename, yaml)
		assert.Nil(t, err)
		defer remove()
		components, err := LoadFromDisk(configPrefix)
		assert.Nil(t, err)
		assert.Len(t, components, 0)
	})

	t.Run("load pluggable components file not exist", func(t *testing.T) {
		components, err := LoadFromDisk("path-not-exists")

		assert.NotNil(t, err)
		assert.Len(t, components, 0)
	})
}
