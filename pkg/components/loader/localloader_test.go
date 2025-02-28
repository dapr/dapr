/*
Copyright 2025 The Dapr Authors
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

package loader

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalLoader_Load(t *testing.T) {
	t.Run("Test Load 1 Component", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		loader := NewLocalLoader("", []string{tmp})
		components, err := loader.Load(context.Background())
		require.NoError(t, err)
		require.Len(t, components, 1)
		require.Equal(t, "statestore", components[0].Name)
	})

	t.Run("Test Load Invalid Component YAML no error no components", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
INVALID YAML
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		loader := NewLocalLoader("", []string{tmp})
		components, err := loader.Load(context.Background())
		require.NoError(t, err)
		require.Empty(t, components)
	})

	t.Run("Test Non Existent Directory", func(t *testing.T) {
		loader := NewLocalLoader("", []string{"/non-existent-directory"})
		_, err := loader.Load(context.Background())
		require.Error(t, err)
	})
}

func TestLocalLoader_Validate(t *testing.T) {
	t.Run("Test Validate", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		loader := NewLocalLoader("", []string{tmp})
		err := loader.Validate(context.Background())
		require.NoError(t, err)
	})

	t.Run("Test Validate Invalid Component YAML no error no components", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
INVALID YAML
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		loader := NewLocalLoader("", []string{tmp})
		err := loader.Validate(context.Background())
		require.NoError(t, err)
	})

	t.Run("Test Validate Non Existent Directory", func(t *testing.T) {
		loader := NewLocalLoader("", []string{"/non-existent-directory"})
		err := loader.Validate(context.Background())
		require.Error(t, err)
	})
}
