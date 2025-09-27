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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	listening   [3]atomic.Bool
	registered  [3]atomic.Bool
	bindingChan [3]chan string
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.bindingChan = [3]chan string{
		make(chan string, 1), make(chan string, 1), make(chan string, 1),
	}

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

	sentry := sentry.New(t)

	h.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	h.operator.AddComponents(compapi.Component{
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

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(h.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, srv, h.operator, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	t.Run("expect 1 component to be loaded", func(t *testing.T) {
		assert.Len(t, h.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		h.expectBinding(t, 0, "binding1")
	})

	t.Run("create a component", func(t *testing.T) {
		h.registered[1].Store(true)
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

		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*5, time.Millisecond*10)
		h.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
		})
	})

	t.Run("create a third component", func(t *testing.T) {
		h.registered[2].Store(true)
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
		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 3)
		}, time.Second*5, time.Millisecond*10)
		h.expectBindings(t, []bindingPair{
			{0, "binding1"},
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting a component should no longer be available", func(t *testing.T) {
		comp := h.operator.Components()[0]
		h.operator.SetComponents(h.operator.Components()[1:]...)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*5, time.Millisecond*10)
		h.registered[0].Store(false)
		h.expectBindings(t, []bindingPair{
			{1, "binding2"},
			{2, "binding3"},
		})
	})

	t.Run("deleting all components should no longer be available", func(t *testing.T) {
		comp1 := h.operator.Components()[0]
		comp2 := h.operator.Components()[1]
		h.operator.SetComponents()
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp1, EventType: operatorv1.ResourceEventType_DELETED})
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp2, EventType: operatorv1.ResourceEventType_DELETED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, h.daprd.GetMetaRegisteredComponents(c, ctx))
		}, time.Second*5, time.Millisecond*10)
		h.registered[1].Store(false)
		h.registered[2].Store(false)
		// Sleep to ensure binding is not triggered.
		time.Sleep(time.Millisecond * 500)
	})

	t.Run("recreating binding should start again", func(t *testing.T) {
		h.registered[0].Store(true)
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
		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
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
