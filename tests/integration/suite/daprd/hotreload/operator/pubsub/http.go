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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
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

	topicChan chan string
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.topicChan = make(chan string, 1)

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
			Name:      "pubsub1",
			Namespace: "default",
		},
		Spec: compapi.ComponentSpec{
			Type:    "pubsub.in-memory",
			Version: "v1",
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

	client := client.HTTP(t)

	t.Run("expect 1 component to be loaded", func(t *testing.T) {
		assert.Len(t, h.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
	})

	t.Run("create a component", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pubsub2",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "pubsub.in-memory",
				Version: "v1",
			},
		}
		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*5, time.Millisecond*10)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
	})

	t.Run("create a third component", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pubsub3",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "pubsub.in-memory",
				Version: "v1",
			},
		}
		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 3)
		}, time.Second*5, time.Millisecond*10)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete a component", func(t *testing.T) {
		comp := h.operator.Components()[1]
		h.operator.SetComponents(h.operator.Components()[0], h.operator.Components()[2])
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*5, time.Millisecond*10)
		h.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete another component", func(t *testing.T) {
		comp := h.operator.Components()[0]
		h.operator.SetComponents(h.operator.Components()[1])
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
		h.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete last component", func(t *testing.T) {
		comp := h.operator.Components()[0]
		h.operator.SetComponents()
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, h.daprd.GetMetaRegisteredComponents(c, ctx))
		}, time.Second*5, time.Millisecond*10)
		h.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		h.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		h.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
	})

	t.Run("recreating pubsub should make it available again", func(t *testing.T) {
		comp := compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pubsub2",
				Namespace: "default",
			},
			Spec: compapi.ComponentSpec{
				Type:    "pubsub.in-memory",
				Version: "v1",
			},
		}
		h.operator.AddComponents(comp)
		h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, h.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
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
