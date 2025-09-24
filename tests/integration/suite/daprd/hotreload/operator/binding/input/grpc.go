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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	listening   [3]atomic.Bool
	registered  [3]atomic.Bool
	bindingChan [3]chan string
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.bindingChan = [3]chan string{
		make(chan string, 1), make(chan string, 1), make(chan string, 1),
	}

	g.registered[0].Store(true)

	srv := app.New(t,
		app.WithOnBindingEventFn(func(ctx context.Context, in *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			switch in.GetName() {
			case "binding1":
				assert.True(t, g.registered[0].Load())
				if g.listening[0].Load() {
					g.listening[0].Store(false)
					g.bindingChan[0] <- in.GetName()
				}
			case "binding2":
				assert.True(t, g.registered[1].Load())
				if g.listening[1].Load() {
					g.listening[1].Store(false)
					g.bindingChan[1] <- in.GetName()
				}
			case "binding3":
				assert.True(t, g.registered[2].Load())
				if g.listening[2].Load() {
					g.listening[2].Store(false)
					g.bindingChan[2] <- in.GetName()
				}
			default:
				assert.Failf(t, "unexpected binding name", "binding name: %s", in.GetName())
			}
			return new(rtv1.BindingEventResponse), nil
		}),
		app.WithListInputBindings(func(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
			return &rtv1.ListInputBindingsResponse{
				Bindings: []string{"binding1", "binding2", "binding3"},
			}, nil
		}),
	)

	sentry := sentry.New(t)

	g.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	g.operator.AddComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "binding1",
			Namespace: "default",
		},
		Spec: compapi.ComponentSpec{
			Type:    "bindings.cron",
			Version: "v1",
			Metadata: []common.NameValuePair{
				{Name: "schedule", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`"@every 300ms"`)},
				}},
				{Name: "direction", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`"input"`)},
				}},
			},
		},
	})

	g.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(g.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv, g.operator, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	t.Run("expect 1 component to be loaded", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*20, time.Millisecond*10)
		g.expectBinding(t, 0, "binding1")
	})

	t.Run("create a component", func(t *testing.T) {
		g.registered[1].Store(true)

		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding2",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.cron",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "schedule", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"@every 300ms"`)},
					}},
					{Name: "direction", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"input"`)},
					}},
				},
			},
		}
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*10)
		g.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
		})
	})

	t.Run("create a third component", func(t *testing.T) {
		g.registered[2].Store(true)
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding3",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.cron",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "schedule", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"@every 300ms"`)},
					}},
					{Name: "direction", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"input"`)},
					}},
				},
			},
		}
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 3)
		}, time.Second*5, time.Millisecond*10)
		g.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting a component should no longer be available", func(t *testing.T) {
		comp := g.operator.Components()[0]
		g.operator.SetComponents(g.operator.Components()[1:]...)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*10)
		g.registered[0].Store(false)
		g.expectBindings(t, []bindingPair{
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting all components should no longer be available", func(t *testing.T) {
		comp1 := g.operator.Components()[0]
		comp2 := g.operator.Components()[1]
		g.operator.SetComponents()
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_DELETED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(c, err)
			assert.Empty(c, resp.GetRegisteredComponents())
		}, time.Second*5, time.Millisecond*10)
		g.registered[1].Store(false)
		g.registered[2].Store(false)
		// Sleep to ensure binding is not triggered.
		time.Sleep(time.Millisecond * 500)
	})

	t.Run("recreating binding should start again", func(t *testing.T) {
		g.registered[0].Store(true)
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "binding1",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "bindings.cron",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{Name: "schedule", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"@every 300ms"`)},
					}},
					{Name: "direction", Value: common.DynamicValue{
						JSON: apiextv1.JSON{Raw: []byte(`"input"`)},
					}},
				},
			},
		}
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*5, time.Millisecond*10)
		g.expectBinding(t, 0, "binding1")
	})
}

type bindingPair struct {
	i int
	b string
}

func (g *grpc) expectBindings(t *testing.T, expected []bindingPair) {
	t.Helper()

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(len(expected))
	for _, e := range expected {
		go func(e bindingPair) {
			g.expectBinding(t, e.i, e.b)
			wg.Done()
		}(e)
	}
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
