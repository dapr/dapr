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

package pubsub

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd

	resDir    string
	topicChan chan string
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.topicChan = make(chan string, 1)

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

	handler := nethttp.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		io.WriteString(w, `[
{
  "pubsubname": "pubsub1",
  "topic": "topic1",
  "route": "route1"
},
{
  "pubsubname": "pubsub2",
	"topic": "topic2",
  "route": "route2"
},
{
  "pubsubname": "pubsub3",
	"topic": "topic3",
  "route": "route3"
}
]`)
	})

	handler.HandleFunc("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if strings.HasPrefix(r.URL.Path, "/route") {
			h.topicChan <- r.URL.Path
		}
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	h.resDir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub1'
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))

	h.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(h.resDir),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	t.Run("expect 1 component to be loaded", func(t *testing.T) {
		assert.Len(t, util.GetMetaComponents(t, ctx, client, h.daprd.HTTPPort()), 1)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
	})

	t.Run("create a component", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub2'
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
	})

	t.Run("create a third component", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub2'
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub3'
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 3)
		}, time.Second*5, time.Millisecond*100)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete a component", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub3'
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete another component", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir, "1.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		h.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete last component", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir, "2.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()))
		}, time.Second*5, time.Millisecond*100)
		h.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
	})

	t.Run("recreating pubsub should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'pubsub2'
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		h.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		h.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
		h.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
	})
}

func (h *http) publishMessage(t *testing.T, ctx context.Context, client *nethttp.Client, pubsub, topic, route string) {
	t.Helper()

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", h.daprd.HTTPPort(), pubsub, topic)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, reqURL, strings.NewReader(`{"status": "completed"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode, reqURL)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(respBody))

	select {
	case topic := <-h.topicChan:
		assert.Equal(t, route, topic)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timed out waiting for topic")
	}
}

func (h *http) publishMessageFails(t *testing.T, ctx context.Context, client *nethttp.Client, pubsub, topic string) {
	t.Helper()

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", h.daprd.HTTPPort(), pubsub, topic)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, reqURL, strings.NewReader(`{"status": "completed"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, nethttp.StatusNotFound, resp.StatusCode, reqURL)
}
