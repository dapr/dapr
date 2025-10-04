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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/kit/ptr"
)

func TestLoad(t *testing.T) {
	t.Run("valid yaml content", func(t *testing.T) {
		tmp := t.TempDir()
		request := NewComponents(Options{
			Paths: []string{tmp},
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
		require.NoError(t, os.WriteFile(filepath.Join(tmp, filename), []byte(yaml), fs.FileMode(0o600)))
		components, err := request.Load(t.Context())
		require.NoError(t, err)
		assert.Len(t, components, 1)
	})

	t.Run("invalid yaml head", func(t *testing.T) {
		tmp := t.TempDir()
		request := NewComponents(Options{
			Paths: []string{tmp},
		})

		filename := "test-component-invalid.yaml"
		yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
name: statestore`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, filename), []byte(yaml), fs.FileMode(0o600)))
		components, err := request.Load(t.Context())
		require.NoError(t, err)
		assert.Empty(t, components)
	})

	t.Run("load components file not exist", func(t *testing.T) {
		request := NewComponents(Options{
			Paths: []string{"test-path-no-exists"},
		})

		components, err := request.Load(t.Context())
		require.Error(t, err)
		assert.Empty(t, components)
	})

	t.Run("error and namespace", func(t *testing.T) {
		buildComp := func(name string, namespace *string, scopes ...string) string {
			var ns string
			if namespace != nil {
				ns = fmt.Sprintf("\n namespace: %s\n", *namespace)
			}
			var scopeS string
			if len(scopes) > 0 {
				scopeS = "\nscopes:\n- " + strings.Join(scopes, "\n- ")
			}
			return fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: %s%s%s`, name, ns, scopeS)
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
			"only return manifests in scope": {
				comps: []string{
					buildComp("comp1", nil, "myappid"),
					buildComp("comp2", nil, "myappid", "anotherappid"),
					buildComp("comp3", nil, "anotherappid"),
				},
				namespace: ptr.Of("foo"),
				expComps: []compapi.Component{
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: ""},
						Scoped:     common.Scoped{Scopes: []string{"myappid"}},
					},
					{
						TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
						ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: ""},
						Scoped:     common.Scoped{Scopes: []string{"myappid", "anotherappid"}},
					},
				},
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
				loader := NewComponents(Options{
					Paths: []string{tmp},
					AppID: "myappid",
				})
				components, err := loader.Load(t.Context())
				assert.Equal(t, test.expErr, err != nil, "%v", err)
				assert.Equal(t, test.expComps, components)
			})
		}
	})
}

func TestLoadWithEnvVarSubstitution(t *testing.T) {
	t.Run("substitute env vars in component manifest", func(t *testing.T) {
		tmp := t.TempDir()

		// Enable environment variable substitution
		t.Setenv("DAPR_ENABLE_RESOURCES_ENV_VAR_SUBSTITUTION", "true")

		yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: redis-store
spec:
  type: state.redis
  metadata:
  - name: redisHost
    value: "{{REDIS_HOST:localhost}}"
  - name: redisPort
    value: "{{REDIS_PORT:6379}}"
  - name: redisPassword
    value: "{{REDIS_PASSWORD}}"
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "redis.yaml"), []byte(yaml), fs.FileMode(0o600)))

		// Set only REDIS_HOST environment variable
		t.Setenv("REDIS_HOST", "redis.production.com")

		loader := NewComponents(Options{Paths: []string{tmp}})
		components, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, components, 1)

		comp := components[0]
		assert.Equal(t, "redis-store", comp.Name)

		// Verify that env var substitution worked
		metadata := comp.Spec.Metadata
		require.Len(t, metadata, 3)

		// REDIS_HOST should be substituted with env var value
		assert.Equal(t, "redisHost", metadata[0].Name)
		assert.Equal(t, "redis.production.com", metadata[0].Value.String())

		// REDIS_PORT should use default value since env var is not set
		assert.Equal(t, "redisPort", metadata[1].Name)
		assert.Equal(t, "6379", metadata[1].Value.String())

		// REDIS_PASSWORD should remain as template since no env var and no default
		assert.Equal(t, "redisPassword", metadata[2].Name)
		assert.Equal(t, "{{REDIS_PASSWORD}}", metadata[2].Value.String())
	})

	t.Run("multiple components with different env vars", func(t *testing.T) {
		tmp := t.TempDir()

		// Enable environment variable substitution
		t.Setenv("DAPR_ENABLE_RESOURCES_ENV_VAR_SUBSTITUTION", "true")

		yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: postgres
spec:
  type: state.postgresql
  metadata:
  - name: connectionString
    value: "host={{DB_HOST:localhost}} port={{DB_PORT:5432}}"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: redis
spec:
  type: state.redis
  metadata:
  - name: host
    value: "{{CACHE_HOST:localhost}}"
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "components.yaml"), []byte(yaml), fs.FileMode(0o600)))

		t.Setenv("DB_HOST", "prod-postgres.example.com")
		t.Setenv("CACHE_HOST", "prod-redis.example.com")

		loader := NewComponents(Options{Paths: []string{tmp}})
		components, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, components, 2)

		// Check postgres component
		postgresComp := components[0]
		assert.Equal(t, "postgres", postgresComp.Name)
		assert.Equal(t, "connectionString", postgresComp.Spec.Metadata[0].Name)
		assert.Equal(t, "host=prod-postgres.example.com port=5432", postgresComp.Spec.Metadata[0].Value.String())

		// Check redis component
		redisComp := components[1]
		assert.Equal(t, "redis", redisComp.Name)
		assert.Equal(t, "host", redisComp.Spec.Metadata[0].Name)
		assert.Equal(t, "prod-redis.example.com", redisComp.Spec.Metadata[0].Value.String())
	})

	t.Run("env var with empty value should be used", func(t *testing.T) {
		tmp := t.TempDir()

		// Enable environment variable substitution
		t.Setenv("DAPR_ENABLE_RESOURCES_ENV_VAR_SUBSTITUTION", "true")

		yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: test-component
spec:
  type: test.type
  metadata:
  - name: value1
    value: "{{EMPTY_VAR:default}}"
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test.yaml"), []byte(yaml), fs.FileMode(0o600)))

		// Set env var to empty string
		t.Setenv("EMPTY_VAR", "")

		loader := NewComponents(Options{Paths: []string{tmp}})
		components, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, components, 1)

		// Empty env var value should be used instead of default
		assert.Equal(t, "", components[0].Spec.Metadata[0].Value.String())
	})

	t.Run("substitution disabled by default", func(t *testing.T) {
		tmp := t.TempDir()

		// Do NOT enable environment variable substitution

		yaml := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: test-component
spec:
  type: test.type
  metadata:
  - name: value1
    value: "{{TEST_VAR:default}}"
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "test.yaml"), []byte(yaml), fs.FileMode(0o600)))

		t.Setenv("TEST_VAR", "actual_value")

		loader := NewComponents(Options{Paths: []string{tmp}})
		components, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, components, 1)

		// Template should remain unchanged since substitution is disabled
		assert.Equal(t, "{{TEST_VAR:default}}", components[0].Spec.Metadata[0].Value.String())
	})
}

func Test_loadWithOrder(t *testing.T) {
	t.Run("no file should return empty set", func(t *testing.T) {
		tmp := t.TempDir()
		d := NewComponents(Options{Paths: []string{tmp}}).(*disk[compapi.Component])
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

		d := NewComponents(Options{Paths: []string{tmp}}).(*disk[compapi.Component])
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

		d := NewComponents(Options{Paths: []string{tmp}}).(*disk[compapi.Component])
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

		d := NewComponents(Options{
			Paths: []string{tmp1, tmp2, tmp3},
		}).(*disk[compapi.Component])
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
