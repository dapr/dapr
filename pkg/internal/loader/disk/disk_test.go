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

func Test_loadWithOrder(t *testing.T) {
	t.Run("no file should return empty set", func(t *testing.T) {
		tmp := t.TempDir()
		d := New[compapi.Component](tmp)
		set, err := d.loadWithOrder()
		require.NoError(t, err)
		assert.Empty(t, set.order)
		assert.Empty(t, set.ts)
	})

	t.Run("single manifest file should return", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		d := New[compapi.Component](tmp)
		set, err := d.loadWithOrder()
		require.NoError(t, err)
		assert.Equal(t, []manifestOrder{
			{dirIndex: 0, fileIndex: 0, manifestIndex: 0},
		}, set.order)
		assert.Len(t, set.ts, 1)
	})

	t.Run("3 manifest file should have order set", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test-component.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		d := New[compapi.Component](tmp)
		set, err := d.loadWithOrder()
		require.NoError(t, err)
		assert.Equal(t, []manifestOrder{
			{dirIndex: 0, fileIndex: 0, manifestIndex: 0},
			{dirIndex: 0, fileIndex: 0, manifestIndex: 1},
			{dirIndex: 0, fileIndex: 0, manifestIndex: 2},
		}, set.order)
		assert.Len(t, set.ts, 3)
	})

	t.Run("3 dirs, 3 files, 3 manifests should return order. Skips manifests of different type", func(t *testing.T) {
		tmp1, tmp2, tmp3 := t.TempDir(), t.TempDir(), t.TempDir()

		for _, dir := range []string{tmp1, tmp2, tmp3} {
			for _, file := range []string{"1.yaml", "2.yaml", "3.yaml"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: statestore
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))
			}
		}

		d := New[compapi.Component](tmp1, tmp2, tmp3)
		set, err := d.loadWithOrder()
		require.NoError(t, err)
		assert.Equal(t, []manifestOrder{
			{dirIndex: 0, fileIndex: 0, manifestIndex: 0},
			{dirIndex: 0, fileIndex: 0, manifestIndex: 2},
			{dirIndex: 0, fileIndex: 1, manifestIndex: 0},
			{dirIndex: 0, fileIndex: 1, manifestIndex: 2},
			{dirIndex: 0, fileIndex: 2, manifestIndex: 0},
			{dirIndex: 0, fileIndex: 2, manifestIndex: 2},

			{dirIndex: 1, fileIndex: 0, manifestIndex: 0},
			{dirIndex: 1, fileIndex: 0, manifestIndex: 2},
			{dirIndex: 1, fileIndex: 1, manifestIndex: 0},
			{dirIndex: 1, fileIndex: 1, manifestIndex: 2},
			{dirIndex: 1, fileIndex: 2, manifestIndex: 0},
			{dirIndex: 1, fileIndex: 2, manifestIndex: 2},

			{dirIndex: 2, fileIndex: 0, manifestIndex: 0},
			{dirIndex: 2, fileIndex: 0, manifestIndex: 2},
			{dirIndex: 2, fileIndex: 1, manifestIndex: 0},
			{dirIndex: 2, fileIndex: 1, manifestIndex: 2},
			{dirIndex: 2, fileIndex: 2, manifestIndex: 0},
			{dirIndex: 2, fileIndex: 2, manifestIndex: 2},
		}, set.order)
		assert.Len(t, set.ts, 18)
	})
}
