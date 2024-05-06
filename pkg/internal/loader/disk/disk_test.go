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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/kit/ptr"
)

func TestLoad(t *testing.T) {
	t.Run("valid yaml content", func(t *testing.T) {
		tmp := t.TempDir()
		request := NewComponents(tmp)
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
		request := NewComponents(tmp)

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
		request := NewComponents("test-path-no-exists")

		components, err := request.Load(context.Background())
		require.Error(t, err)
		assert.Empty(t, components)
	})

	t.Run("error and namespace", func(t *testing.T) {
		buildComp := func(name string, namespace *string) string {
			var ns string
			if namespace != nil {
				ns = fmt.Sprintf("\n namespace: %s\n", *namespace)
			}
			return fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: %s%s`, name, ns)
		}

		tests := map[string]struct {
			comps     []string
			namespace *string
			expComps  []compapi.Component
			expErr    bool
		}{
			"if no manifests, return nothing": {
				comps:     nil,
				namespace: nil,
				expComps:  []compapi.Component{},
				expErr:    false,
			},
			"if single manifest, return manifest": {
				comps:     []string{buildComp("comp1", nil)},
				namespace: nil,
				expComps: []compapi.Component{
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
					},
				},
				expErr: false,
			},
			"if namespace not set, return all manifests": {
				comps: []string{
					buildComp("comp1", nil),
					buildComp("comp2", ptr.Of("default")),
					buildComp("comp3", ptr.Of("foo")),
				},
				namespace: nil,
				expComps: []compapi.Component{
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
					},
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: "default"},
					},
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp3", Namespace: "foo"},
					},
				},
			},
			"if namespace set, return only manifests in that namespace": {
				comps: []string{
					buildComp("comp1", nil),
					buildComp("comp2", ptr.Of("default")),
					buildComp("comp3", ptr.Of("foo")),
					buildComp("comp4", ptr.Of("foo")),
					buildComp("comp5", ptr.Of("bar")),
				},
				namespace: ptr.Of("foo"),
				expComps: []compapi.Component{
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: ""},
					},
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp3", Namespace: "foo"},
					},
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp4", Namespace: "foo"},
					},
				},
			},
			"if duplicate manifest, return error": {
				comps: []string{
					buildComp("comp1", nil),
					buildComp("comp1", nil),
				},
				namespace: nil,
				expComps:  nil,
				expErr:    true,
			},
			"ignore duplicate manifest if namespace doesn't match": {
				comps: []string{
					buildComp("comp1", ptr.Of("foo")),
					buildComp("comp1", ptr.Of("bar")),
				},
				namespace: ptr.Of("foo"),
				expComps: []compapi.Component{
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: "foo"},
					},
				},
				expErr: false,
			},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				tmp := t.TempDir()
				b := strings.Join(test.comps, "\n---\n")
				require.NoError(t, os.WriteFile(filepath.Join(tmp, "components.yaml"), []byte(b), fs.FileMode(0o600)))

				t.Setenv("NAMESPACE", "")
				if test.namespace != nil {
					t.Setenv("NAMESPACE", *test.namespace)
				}
				loader := NewComponents(tmp)
				components, err := loader.Load(context.Background())
				assert.Equal(t, test.expErr, err != nil, "%v", err)
				assert.Equal(t, test.expComps, components)
			})
		}
	})
}

func Test_loadWithOrder(t *testing.T) {
	t.Run("no file should return empty set", func(t *testing.T) {
		tmp := t.TempDir()
		d := NewComponents(tmp).(*disk[compapi.Component])
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

		d := NewComponents(tmp).(*disk[compapi.Component])
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
  name: foo1
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo2
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo3
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))

		d := NewComponents(tmp).(*disk[compapi.Component])
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
  name: foo1
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: foo2
spec:
  type: state.couchbase
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo3
spec:
  type: state.couchbase
`), fs.FileMode(0o600)))
			}
		}

		d := NewComponents(tmp1, tmp2, tmp3).(*disk[compapi.Component])
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
