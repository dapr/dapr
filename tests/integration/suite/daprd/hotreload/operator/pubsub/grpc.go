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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	topicChan chan string
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.topicChan = make(chan string, 1)

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

	srv := app.New(t,
		app.WithOnTopicEventFn(func(_ context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			g.topicChan <- in.GetPath()
			return new(rtv1.TopicEventResponse), nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{PubsubName: "pubsub1", Topic: "topic1", Routes: &rtv1.TopicRoutes{Default: "/route1"}},
					{PubsubName: "pubsub2", Topic: "topic2", Routes: &rtv1.TopicRoutes{Default: "/route2"}},
					{PubsubName: "pubsub3", Topic: "topic3", Routes: &rtv1.TopicRoutes{Default: "/route3"}},
				},
			}, nil
		}),
	)

	g.operator.AddComponents(compapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pubsub1",
			Namespace: "default",
		},
		Spec: compapi.ComponentSpec{
			Type:    "pubsub.in-memory",
			Version: "v1",
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
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(t, resp.GetRegisteredComponents(), 1)
		g.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		g.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		g.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
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
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*10)
		g.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		g.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
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
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 3)
		}, time.Second*5, time.Millisecond*10)
		g.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		g.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
		g.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete a component", func(t *testing.T) {
		comp := g.operator.Components()[1]
		g.operator.SetComponents(g.operator.Components()[0], g.operator.Components()[2])
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*5, time.Millisecond*10)
		g.publishMessage(t, ctx, client, "pubsub1", "topic1", "/route1")
		g.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		g.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete another component", func(t *testing.T) {
		comp := g.operator.Components()[0]
		g.operator.SetComponents(g.operator.Components()[1])
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*5, time.Millisecond*10)
		g.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		g.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		g.publishMessage(t, ctx, client, "pubsub3", "topic3", "/route3")
	})

	t.Run("delete last component", func(t *testing.T) {
		comp := g.operator.Components()[0]
		g.operator.SetComponents()
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Empty(c, resp.GetRegisteredComponents())
		}, time.Second*5, time.Millisecond*10)
		g.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		g.publishMessageFails(t, ctx, client, "pubsub2", "topic2")
		g.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
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
		g.operator.AddComponents(comp)
		g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			require.NoError(t, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*5, time.Millisecond*10)
		g.publishMessageFails(t, ctx, client, "pubsub1", "topic1")
		g.publishMessage(t, ctx, client, "pubsub2", "topic2", "/route2")
		g.publishMessageFails(t, ctx, client, "pubsub3", "topic3")
	})
}

func (g *grpc) publishMessage(t *testing.T, ctx context.Context, client rtv1.DaprClient, pubsub, topic, route string) {
	t.Helper()

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: pubsub,
		Topic:      topic,
		Data:       []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)

	select {
	case topic := <-g.topicChan:
		assert.Equal(t, route, topic)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timed out waiting for topic")
	}
}

func (g *grpc) publishMessageFails(t *testing.T, ctx context.Context, client rtv1.DaprClient, pubsub, topic string) {
	t.Helper()

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: pubsub,
		Topic:      topic,
		Data:       []byte(`{"status": "completed"}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
