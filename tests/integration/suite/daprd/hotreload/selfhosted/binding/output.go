/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package binding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(output))
}

type output struct {
	daprd *daprd.Daprd

	resDir      string
	bindingDir1 string
	bindingDir2 string
	bindingDir3 string
}

func (o *output) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	o.resDir = t.TempDir()
	o.bindingDir1, o.bindingDir2, o.bindingDir3 = t.TempDir(), t.TempDir(), t.TempDir()

	o.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(o.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(o.daprd),
	}
}

func (o *output) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)
	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, util.GetMetaComponents(t, ctx, client, o.daprd.HTTPPort()))
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(o.resDir, "1.yaml"), []byte(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
`, o.bindingDir1)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		o.postBinding(t, ctx, client, "binding1", "file1", "data1")
		o.postBindingFail(t, ctx, client, "binding2")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir1, "file1", "data1")
	})

	t.Run("adding another component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(o.resDir, "1.yaml"), []byte(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding2'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
`, o.bindingDir1, o.bindingDir2)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		o.postBinding(t, ctx, client, "binding1", "file2", "data2")
		o.postBinding(t, ctx, client, "binding2", "file1", "data1")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir1, "file2", "data2")
		o.assertFile(t, o.bindingDir2, "file1", "data1")
	})

	t.Run("adding 3rd component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(o.resDir, "2.yaml"), []byte(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding3'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
`, o.bindingDir3)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()), 3)
		}, time.Second*5, time.Millisecond*100)
		o.postBinding(t, ctx, client, "binding1", "file3", "data3")
		o.postBinding(t, ctx, client, "binding2", "file2", "data2")
		o.postBinding(t, ctx, client, "binding3", "file1", "data1")
		o.assertFile(t, o.bindingDir1, "file3", "data3")
		o.assertFile(t, o.bindingDir2, "file2", "data2")
		o.assertFile(t, o.bindingDir3, "file1", "data1")
	})

	t.Run("deleting component makes it no longer available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(o.resDir, "1.yaml"), []byte(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding2'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
`, o.bindingDir2)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		o.postBindingFail(t, ctx, client, "binding1")
		assert.NoFileExists(t, filepath.Join(o.bindingDir1, "file4"))
		o.postBinding(t, ctx, client, "binding2", "file3", "data3")
		o.postBinding(t, ctx, client, "binding3", "file2", "data2")
		o.assertFile(t, o.bindingDir2, "file3", "data3")
		o.assertFile(t, o.bindingDir3, "file2", "data2")
	})

	t.Run("deleting component files are no longer available", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(o.resDir, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(o.resDir, "2.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()))
		}, time.Second*5, time.Millisecond*100)
		o.postBindingFail(t, ctx, client, "binding1")
		o.postBindingFail(t, ctx, client, "binding2")
		o.postBindingFail(t, ctx, client, "binding3")
	})

	t.Run("recreating binding component should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(o.resDir, "2.yaml"), []byte(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding2'
spec:
 type: bindings.localstorage
 version: v1
 metadata:
 - name: rootPath
   value: '%s'
`, o.bindingDir2)), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, o.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		o.postBinding(t, ctx, client, "binding2", "file5", "data5")
		o.postBindingFail(t, ctx, client, "binding1")
		o.postBindingFail(t, ctx, client, "binding3")
		o.assertFile(t, o.bindingDir2, "file5", "data5")
	})
}

func (o *output) postBinding(t *testing.T, ctx context.Context, client *http.Client, binding, file, data string) {
	t.Helper()

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", o.daprd.HTTPPort(), binding)
	body := fmt.Sprintf(`{"operation":"create","data":"%s","metadata":{"fileName":"%s"}}`, data, file)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respbody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(respbody))
}

func (o *output) postBindingFail(t *testing.T, ctx context.Context, client *http.Client, binding string) {
	t.Helper()

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", o.daprd.HTTPPort(), binding)
	body := `{"operation":"create","data":"foo","metadata":{"fileName":"foo"}}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respbody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode, string(respbody))
}

func (o *output) assertFile(t *testing.T, dir, file, expData string) {
	t.Helper()
	fdata, err := os.ReadFile(filepath.Join(dir, file))
	require.NoError(t, err)
	assert.Equal(t, expData, string(fdata), fdata)
}
