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

package selfhosted

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtpbv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(state))
}

type state struct {
	daprd *daprd.Daprd

	resDir1 string
	resDir2 string
	resDir3 string
}

func (s *state) Setup(t *testing.T) []framework.Option {
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

	s.resDir1, s.resDir2, s.resDir3 = t.TempDir(), t.TempDir(), t.TempDir()

	s.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(s.resDir1, s.resDir2, s.resDir3),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, util.GetMetaComponents(t, ctx, client, s.daprd.HTTPPort()))
		s.writeExpectError(t, ctx, client, "123", http.StatusInternalServerError)
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '123'
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		resp := util.GetMetaComponents(t, ctx, client, s.daprd.HTTPPort())
		assert.ElementsMatch(t, []*rtpbv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, resp)

		s.writeRead(t, ctx, client, "123")
	})

	t.Run("adding a second and third component should also become available", func(t *testing.T) {
		// After a single state store exists, Dapr returns a Bad Request response
		// rather than an Internal Server Error when writing to a non-existent
		// state store.
		s.writeExpectError(t, ctx, client, "abc", http.StatusBadRequest)
		s.writeExpectError(t, ctx, client, "xyz", http.StatusBadRequest)

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir3, "3.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'xyz'
spec:
 type: state.in-memory
 version: v1
 metadata: []
 `), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 3)
		}, time.Second*5, time.Millisecond*100)
		resp := util.GetMetaComponents(t, ctx, client, s.daprd.HTTPPort())
		assert.ElementsMatch(t, []*rtpbv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
			{
				Name: "abc", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
			{
				Name: "xyz", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, resp)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
	})

	tmpDir := t.TempDir()
	t.Run("changing the type of a state store should update the component and still be available", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.sqlite
 version: v1
 metadata:
 - name: connectionString
   value: %s/db.sqlite
`, tmpDir)), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{
					Name: "123", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
	})

	t.Run("updating multiple state stores should be updated, and multiple components in a single file", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeExpectError(t, ctx, client, "foo", http.StatusBadRequest)

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.sqlite
 version: v1
 metadata:
 - name: connectionString
   value: %s/db.sqlite
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'foo'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`, s.resDir1)), 0o600))

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'abc'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "abc", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	t.Run("renaming a component should close the old name, and open the new one", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "abc")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'bar'
spec:
 type: state.in-memory
 version: v1
 metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "bar", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "bar")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	tmpDir = t.TempDir()
	t.Run("deleting a component (through type update) should delete the components", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeRead(t, ctx, client, "bar")
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		secPath := filepath.Join(tmpDir, "foo")
		require.NoError(t, os.WriteFile(secPath, []byte(`{}`), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'bar'
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`, secPath)), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{Name: "bar", Type: "secretstores.local.file", Version: "v1"},
				{
					Name: "123", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
				{
					Name: "xyz", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
				{
					Name: "foo", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, client, "123")
		s.writeExpectError(t, ctx, client, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")
	})

	t.Run("deleting all components should result in no components remaining", func(t *testing.T) {
		s.writeRead(t, ctx, client, "123")
		s.writeExpectError(t, ctx, client, "bar", http.StatusBadRequest)
		s.writeRead(t, ctx, client, "xyz")
		s.writeRead(t, ctx, client, "foo")

		require.NoError(t, os.Remove(filepath.Join(s.resDir1, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(s.resDir3, "3.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort())
			assert.ElementsMatch(c, []*rtpbv1.RegisteredComponents{
				{Name: "bar", Type: "secretstores.local.file", Version: "v1"},
			}, resp)
		}, time.Second*10, time.Millisecond*100)

		s.writeExpectError(t, ctx, client, "123", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "bar", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "xyz", http.StatusInternalServerError)
		s.writeExpectError(t, ctx, client, "foo", http.StatusInternalServerError)
	})

	t.Run("recreate state component should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '123'
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)

		s.writeRead(t, ctx, client, "123")
	})
}

func (s *state) writeExpectError(t *testing.T, ctx context.Context, client *http.Client, compName string, expCode int) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), compName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, nil)
	require.NoError(t, err)
	s.doReq(t, client, req, expCode, fmt.Sprintf(
		`\{"errorCode":"(ERR_STATE_STORE_NOT_CONFIGURED|ERR_STATE_STORE_NOT_FOUND)","message":"state store %s (is not configured|is not found)","details":\[.*\]\}`,
		compName))
}

func (s *state) writeRead(t *testing.T, ctx context.Context, client *http.Client, compName string) {
	t.Helper()

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", s.daprd.HTTPPort(), url.QueryEscape(compName))
	getURL := fmt.Sprintf("%s/foo", postURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL,
		strings.NewReader(`[{"key": "foo", "value": "bar"}]`))
	require.NoError(t, err)
	s.doReq(t, client, req, http.StatusNoContent, "")

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, client, req, http.StatusOK, `"bar"`)
}

func (s *state) doReq(t *testing.T, client *http.Client, req *http.Request, expCode int, expBody string) {
	t.Helper()

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
