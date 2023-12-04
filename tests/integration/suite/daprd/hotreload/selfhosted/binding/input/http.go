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

package input

import (
	"context"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

	resDir      string
	listening   [3]atomic.Bool
	registered  [3]atomic.Bool
	bindingChan [3]chan string
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.bindingChan = [3]chan string{
		make(chan string, 1), make(chan string, 1), make(chan string, 1),
	}

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

	h.resDir = t.TempDir()

	h.registered[0].Store(true)

	handler := nethttp.NewServeMux()
	handler.HandleFunc("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		if strings.HasPrefix(r.URL.Path, "/binding") {
			switch path := r.URL.Path; path {
			case "/binding1":
				assert.True(t, h.registered[0].Load())
				if h.listening[0].Load() {
					h.listening[0].Store(false)
					h.bindingChan[0] <- path
				}
			case "/binding2":
				assert.True(t, h.registered[1].Load())
				if h.listening[1].Load() {
					h.listening[1].Store(false)
					h.bindingChan[1] <- path
				}
			case "/binding3":
				assert.True(t, h.registered[2].Load())
				if h.listening[2].Load() {
					h.listening[2].Store(false)
					h.bindingChan[2] <- path
				}
			default:
				assert.Failf(t, "unexpected binding name", "binding name: %s", path)
			}
		}
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 300ms"
  - name: direction
    value: "input"
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
		h.expectBinding(t, 0, "binding1")
	})

	t.Run("create a component", func(t *testing.T) {
		h.registered[1].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding2'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 300ms"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		h.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
		})
	})

	t.Run("create a third component", func(t *testing.T) {
		h.registered[2].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 300ms"
  - name: direction
    value: "input"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding3'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 300ms"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 3)
		}, time.Second*5, time.Millisecond*100)
		h.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting a component should no longer be available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding3'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1ms"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100)
		h.registered[0].Store(false)
		h.expectBindings(t, []bindingPair{
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting all components should no longer be available", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(h.resDir, "2.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()))
		}, time.Second*5, time.Millisecond*100)
		h.registered[1].Store(false)
		h.registered[2].Store(false)
		// Sleep to ensure binding is not triggered.
		time.Sleep(time.Millisecond * 500)
	})

	t.Run("recreating binding should start again", func(t *testing.T) {
		h.registered[0].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 300ms"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(c, ctx, client, h.daprd.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100)
		h.expectBinding(t, 0, "binding1")
	})
}

func (h *http) expectBindings(t *testing.T, expected []bindingPair) {
	t.Helper()

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(len(expected))
	for _, e := range expected {
		go func(e bindingPair) {
			h.expectBinding(t, e.i, e.b)
			wg.Done()
		}(e)
	}
}

func (h *http) expectBinding(t *testing.T, i int, binding string) {
	t.Helper()

	h.listening[i].Store(true)
	select {
	case got := <-h.bindingChan[i]:
		assert.Equal(t, "/"+binding, got)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timed out waiting for binding event")
	}
}
