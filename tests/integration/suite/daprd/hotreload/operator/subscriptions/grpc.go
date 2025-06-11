/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package subscriptions

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	sub              *subscriber.Subscriber
	daprd            *daprd.Daprd
	operator         *operator.Operator
	pubsub1, pubsub2 compapi.Component
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)
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

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(g.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	g.pubsub1 = compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub0", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	g.pubsub2 = compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub1", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	g.operator.AddComponents(g.pubsub1, g.pubsub2)

	return []framework.Option{
		framework.WithProcesses(g.sub, sentry, g.operator, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaRegisteredComponents(t, ctx), 2)
		assert.Empty(c, g.daprd.GetMetaSubscriptions(t, ctx))
	}, time.Second*5, time.Millisecond*10)

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, newReq("pubsub0", "a"))

	sub1 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub0", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	g.operator.AddSubscriptions(sub1)
	g.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub1,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "a"))

	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, newReq("pubsub0", "b"))
	sub2 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "b",
			Routes: subapi.Routes{Default: "/b"},
		},
	}
	g.operator.AddSubscriptions(sub2)
	g.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub2,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "a"))
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "b"))

	g.operator.SetSubscriptions(sub2)
	g.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub1,
		EventType:    operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "b"))
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, newReq("pubsub0", "a"))

	sub3 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub2", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub1", Topic: "c",
			Routes: subapi.Routes{Default: "/c"},
		},
	}
	g.operator.AddSubscriptions(sub3)
	g.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub3,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "b"))
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub1", "c"))

	sub2.Spec.Topic = "d"
	sub2.Spec.Routes.Default = "/d"
	g.operator.SetSubscriptions(sub2, sub3)
	g.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub2,
		EventType:    operatorv1.ResourceEventType_UPDATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := g.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		if assert.Len(c, resp.GetSubscriptions(), 2) {
			assert.Equal(c, "c", resp.GetSubscriptions()[0].GetTopic())
			assert.Equal(c, "d", resp.GetSubscriptions()[1].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, newReq("pubsub0", "b"))
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub0", "d"))
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub1", "c"))

	g.operator.SetComponents()
	g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{
		Component: &g.pubsub1,
		EventType: operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, g.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishError(t, ctx, g.daprd, newReq("pubsub0", "d"))
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, newReq("pubsub1", "c"))

	g.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{
		Component: &g.pubsub2,
		EventType: operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, g.daprd.GetMetaRegisteredComponents(t, ctx))
	}, time.Second*5, time.Millisecond*10)
	g.sub.ExpectPublishError(t, ctx, g.daprd, newReq("pubsub0", "d"))
	g.sub.ExpectPublishError(t, ctx, g.daprd, newReq("pubsub1", "c"))
}
