/*
Copyright 2024 The Dapr Authors
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

package disk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

func TestLoad(t *testing.T) {
	t.Run("valid yaml content", func(t *testing.T) {
		tmp := t.TempDir()
		request := New[compapi.Component](tmp)
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
		require.NoError(t, os.WriteFile(filepath.Join(tmp, filename), []byte(yaml), fs.FileMode(0o600)))
		components, err := request.Load(context.Background())
		require.NoError(t, err)
		assert.Len(t, components, 1)
	})

	t.Run("invalid yaml head", func(t *testing.T) {
		tmp := t.TempDir()
		request := New[compapi.Component](tmp)

		filename := "test-component-invalid.yaml"
		yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: statestore`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, filename), []byte(yaml), fs.FileMode(0o600)))
		components, err := request.Load(context.Background())
		require.NoError(t, err)
		assert.Empty(t, components)
	})

	t.Run("load components file not exist", func(t *testing.T) {
		request := New[compapi.Component]("test-path-no-exists")

		components, err := request.Load(context.Background())
		require.Error(t, err)
		assert.Empty(t, components)
	})
}
