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
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpcapp"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd *daprd.Daprd

	resDir      string
	listening   [3]atomic.Bool
	registered  [3]atomic.Bool
	bindingChan [3]chan string
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.bindingChan = [3]chan string{
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

	g.resDir = t.TempDir()

	g.registered[0].Store(true)

	srv := grpcapp.New(t,
		grpcapp.WithOnBindingEventFn(func(ctx context.Context, in *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			switch in.Name {
			case "binding1":
				assert.True(t, g.registered[0].Load())
				if g.listening[0].Load() {
					g.listening[0].Store(false)
					g.bindingChan[0] <- string(in.Name)
				}
			case "binding2":
				assert.True(t, g.registered[1].Load())
				if g.listening[1].Load() {
					g.listening[1].Store(false)
					g.bindingChan[1] <- string(in.Name)
				}
			case "binding3":
				assert.True(t, g.registered[2].Load())
				if g.listening[2].Load() {
					g.listening[2].Store(false)
					g.bindingChan[2] <- string(in.Name)
				}
			default:
				assert.Failf(t, "unexpected binding name", "binding name: %s", in.Name)
			}
			return new(rtv1.BindingEventResponse), nil
		}),
		grpcapp.WithListInputBindings(func(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
			return &rtv1.ListInputBindingsResponse{
				Bindings: []string{"binding1", "binding2", "binding3"},
			}, nil
		}),
	)

	require.NoError(t, os.WriteFile(filepath.Join(g.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: "input"
`), 0o600))

	g.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(g.resDir),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	conn, err := googlegrpc.DialContext(ctx, fmt.Sprintf("localhost:%d", g.daprd.GRPCPort()),
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
		googlegrpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := rtv1.NewDaprClient(conn)

	t.Run("expect 1 component to be loaded", func(t *testing.T) {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(t, resp.RegisteredComponents, 1)
		g.expectBinding(t, 0, "binding1")
	})

	t.Run("create a component", func(t *testing.T) {
		g.registered[1].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(g.resDir, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding2'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.RegisteredComponents, 2)
		}, time.Second*5, time.Millisecond*100)
		g.expectBinding(t, 0, "binding1")
		g.expectBinding(t, 1, "binding2")
	})

	t.Run("create a third component", func(t *testing.T) {
		g.registered[2].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(g.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
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
    value: "@every 1s"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.RegisteredComponents, 3)
		}, time.Second*5, time.Millisecond*100)
		g.expectBinding(t, 0, "binding1")
		g.expectBinding(t, 1, "binding2")
		g.expectBinding(t, 2, "binding3")
	})

	t.Run("deleting a component should no longer be available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(g.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding3'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.RegisteredComponents, 2)
		}, time.Second*5, time.Millisecond*100)
		g.registered[0].Store(false)
		g.expectBinding(t, 1, "binding2")
		g.expectBinding(t, 2, "binding3")
	})

	t.Run("deleting all components should no longer be available", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(g.resDir, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(g.resDir, "2.yaml")))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			if assert.NoError(c, err) {
				assert.Len(c, resp.RegisteredComponents, 0)
			}
		}, time.Second*5, time.Millisecond*100)
		g.registered[1].Store(false)
		g.registered[2].Store(false)
		// Sleep to ensure binding is not triggered.
		time.Sleep(time.Second * 2)
	})

	t.Run("recreating binding should start again", func(t *testing.T) {
		g.registered[0].Store(true)
		require.NoError(t, os.WriteFile(filepath.Join(g.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'binding1'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: "input"
`), 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			if assert.NoError(c, err) {
				assert.Len(c, resp.RegisteredComponents, 1)
			}
		}, time.Second*5, time.Millisecond*100)
		g.expectBinding(t, 0, "binding1")
	})
}

func (g *grpc) expectBinding(t *testing.T, i int, binding string) {
	t.Helper()

	g.listening[i].Store(true)
	select {
	case got := <-g.bindingChan[i]:
		assert.Equal(t, binding, got)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timed out waiting for binding event")
	}
}
