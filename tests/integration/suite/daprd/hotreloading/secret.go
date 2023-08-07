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

package hotreloading

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secret))
}

type secret struct {
	daprd  *procdaprd.Daprd
	client *http.Client

	resDir1 string
	resDir2 string
	resDir3 string
}

func (s *secret) Setup(t *testing.T) []framework.Option {
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
	s.client = util.HTTPClient(t)

	s.daprd = procdaprd.New(t,
		procdaprd.WithConfigs(configFile),
		procdaprd.WithResourcesDir(s.resDir1, s.resDir2, s.resDir3),
		procdaprd.WithExecOptions(exec.WithEnvVars(
			"FOO_SEC_1", "bar1",
			"FOO_SEC_2", "bar2",
			"FOO_SEC_3", "bar3",
			"BAR_SEC_1", "baz1",
			"BAR_SEC_2", "baz2",
			"BAR_SEC_3", "baz3",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *secret) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Len(t, s.getMetaResponse(t, ctx), 0)
		s.readExpectError(t, ctx, "123", "foo", http.StatusInternalServerError)
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: secretstores.local.env
 version: v1
 metadata:
 - name: prefix
   value: FOO_
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.getMetaResponse(t, ctx), 1)
		}, time.Second*5, time.Millisecond*100)
		resp := s.getMetaResponse(t, ctx)
		require.Len(t, resp, 1)

		assert.Equal(t, &runtimev1pb.RegisteredComponents{
			Name:    "123",
			Type:    "secretstores.local.env",
			Version: "v1",
		}, resp[0])

		s.read(t, ctx, "123", "SEC_1", "bar1")
		s.read(t, ctx, "123", "SEC_2", "bar2")
		s.read(t, ctx, "123", "SEC_3", "bar3")
	})

	//	t.Run("adding a second and third component should also become available", func(t *testing.T) {
	//		// After a single secret store exists, Dapr returns a Bad Request response
	//		// rather than an Internal Server Error when writing to a non-existent
	//		// secret store.
	//		s.writeExpectError(t, ctx, "abc", http.StatusBadRequest)
	//		s.writeExpectError(t, ctx, "xyz", http.StatusBadRequest)
	//
	//		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'abc'
	// spec:
	// type: secret.in-memory
	// version: v1
	// metadata: []
	// `), 0o600))
	//
	//	require.NoError(t, os.WriteFile(filepath.Join(s.resDir3, "3.yaml"), []byte(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'xyz'
	// spec:
	// type: secret.in-memory
	// version: v1
	// metadata: []
	// `), 0o600))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			assert.Len(c, s.getMetaResponse(t, ctx), 3)
	//		}, time.Second*5, time.Millisecond*100)
	//		resp := s.getMetaResponse(t, ctx)
	//		require.Len(t, resp, 3)
	//
	//		assert.ElementsMatch(t, []*runtimev1pb.RegisteredComponents{
	//			&runtimev1pb.RegisteredComponents{
	//				Name: "123", Type: "secret.in-memory", Version: "v1",
	//				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			&runtimev1pb.RegisteredComponents{
	//				Name: "abc", Type: "secret.in-memory", Version: "v1",
	//				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			&runtimev1pb.RegisteredComponents{
	//				Name: "xyz", Type: "secret.in-memory", Version: "v1",
	//				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//		}, resp)
	//
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//	})
	//
	//	t.Run("changing the type of a secret store should update the component and still be available", func(t *testing.T) {
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//
	//		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'abc'
	// spec:
	// type: secret.sqlite
	// version: v1
	// metadata:
	//   - name: connectionString
	//     value: %s/db.sqlite
	//
	// `, s.resDir2)), 0o600))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			resp := s.getMetaResponse(c, ctx)
	//			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "123", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "abc", Type: "secret.sqlite", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "xyz", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			}, resp)
	//		}, time.Second*5, time.Millisecond*100)
	//
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//	})
	//
	//	t.Run("updating multiple secret stores should be updated, and multiple components in a single file", func(t *testing.T) {
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeExpectError(t, ctx, "foo", http.StatusBadRequest)
	//
	//		require.NoError(t, os.WriteFile(filepath.Join(s.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: '123'
	// spec:
	// type: secret.sqlite
	// version: v1
	// metadata:
	//   - name: connectionString
	//     value: %s/db.sqlite
	//
	// ---
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'foo'
	// spec:
	// type: secret.in-memory
	// version: v1
	// metadata: []
	// `, s.resDir1)), 0o600))
	//
	//	require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'abc'
	// spec:
	// type: secret.in-memory
	// version: v1
	// metadata: []
	// `), 0o600))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			resp := s.getMetaResponse(c, ctx)
	//			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "123", Type: "secret.sqlite", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "abc", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "xyz", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "foo", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			}, resp)
	//		}, time.Second*5, time.Millisecond*100)
	//
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//	})
	//
	//	t.Run("renaming a component should close the old name, and open the new one", func(t *testing.T) {
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "abc")
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//
	//		require.NoError(t, os.WriteFile(filepath.Join(s.resDir2, "2.yaml"), []byte(`
	//
	// apiVersion: dapr.io/v1alpha1
	// kind: Component
	// metadata:
	// name: 'bar'
	// spec:
	// type: secret.in-memory
	// version: v1
	// metadata: []
	// `), 0o600))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			resp := s.getMetaResponse(c, ctx)
	//			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "123", Type: "secret.sqlite", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "bar", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "xyz", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "foo", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			}, resp)
	//		}, time.Second*5, time.Millisecond*100)
	//
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "bar")
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//	})
	//
	//	t.Run("deleting a component file should delete the components", func(t *testing.T) {
	//		s.writeRead(t, ctx, "123")
	//		s.writeRead(t, ctx, "bar")
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//
	//		require.NoError(t, os.Remove(filepath.Join(s.resDir2, "2.yaml")))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			resp := s.getMetaResponse(c, ctx)
	//			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "123", Type: "secret.sqlite", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "xyz", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//				&runtimev1pb.RegisteredComponents{
	//					Name: "foo", Type: "secret.in-memory", Version: "v1",
	//					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"}},
	//			}, resp)
	//		}, time.Second*5, time.Millisecond*100)
	//
	//		s.writeRead(t, ctx, "123")
	//		s.writeExpectError(t, ctx, "bar", http.StatusBadRequest)
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//	})
	//
	//	t.Run("deleting all components should result in no components remaining", func(t *testing.T) {
	//		s.writeRead(t, ctx, "123")
	//		s.writeExpectError(t, ctx, "bar", http.StatusBadRequest)
	//		s.writeRead(t, ctx, "xyz")
	//		s.writeRead(t, ctx, "foo")
	//
	//		require.NoError(t, os.Remove(filepath.Join(s.resDir1, "1.yaml")))
	//		require.NoError(t, os.Remove(filepath.Join(s.resDir3, "3.yaml")))
	//
	//		require.EventuallyWithT(t, func(c *assert.CollectT) {
	//			resp := s.getMetaResponse(c, ctx)
	//			assert.ElementsMatch(c, []*runtimev1pb.RegisteredComponents{}, resp)
	//		}, time.Second*5, time.Millisecond*100)
	//
	//		s.writeExpectError(t, ctx, "123", http.StatusInternalServerError)
	//		s.writeExpectError(t, ctx, "bar", http.StatusInternalServerError)
	//		s.writeExpectError(t, ctx, "xyz", http.StatusInternalServerError)
	//		s.writeExpectError(t, ctx, "foo", http.StatusInternalServerError)
	//	})
}

func (s *secret) getMetaResponse(t require.TestingT, ctx context.Context) []*runtimev1pb.RegisteredComponents {
	metaURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", s.daprd.HTTPPort())
	type metaResponse struct {
		Comps []*runtimev1pb.RegisteredComponents `json:"components,omitempty"`
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	require.NoError(t, err)
	resp, err := s.client.Do(req)
	require.NoError(t, err)

	var meta metaResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&meta))

	return meta.Comps
}

func (s *secret) readExpectError(t *testing.T, ctx context.Context, compName, key string, expCode int) {
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/%s",
		s.daprd.HTTPPort(), url.QueryEscape(compName), url.QueryEscape(key))
	fmt.Printf(">>%s\n", getURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, expCode, fmt.Sprintf(
		`\{"errorCode":"(ERR_SECRET_STORES_NOT_CONFIGURED|ERR_SECRET_STORES_NOT_FOUND)","message":"secret store (is not configured|%s is not found)"\}`,
		compName))
}

func (s *secret) read(t *testing.T, ctx context.Context, compName, key, expValue string) {
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secret/%s/%s", s.daprd.HTTPPort(), url.QueryEscape(compName), url.QueryEscape(key))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	s.doReq(t, req, http.StatusOK, expValue)
}

func (s *secret) doReq(t *testing.T, req *http.Request, expCode int, expBody string) {
	resp, err := s.client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
