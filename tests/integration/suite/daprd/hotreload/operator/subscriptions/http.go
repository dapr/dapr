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
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	sub              *subscriber.Subscriber
	daprd            *daprd.Daprd
	operator         *operator.Operator
	pubsub1, pubsub2 compapi.Component
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t,
		subscriber.WithRoutes("/a", "/b", "/c", "/d"),
	)
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

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(h.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	h.pubsub1 = compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub0", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	h.pubsub2 = compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub1", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	h.operator.AddComponents(h.pubsub1, h.pubsub2)

	return []framework.Option{
		framework.WithProcesses(h.sub, sentry, h.operator, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaRegisteredComponents(t, ctx), 2)
		assert.Empty(c, h.daprd.GetMetaSubscriptions(t, ctx))
	}, time.Second*5, time.Millisecond*10)

	newReq := func(daprd *daprd.Daprd, pubsubName, topic string) subscriber.PublishRequest {
		return subscriber.PublishRequest{
			Daprd:      daprd,
			PubSubName: pubsubName,
			Topic:      topic,
			Data:       `{"status": "completed"}`,
		}
	}
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	sub1 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub0", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	h.operator.AddSubscriptions(sub1)
	h.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub1,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	sub2 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "b",
			Routes: subapi.Routes{Default: "/b"},
		},
	}
	h.operator.AddSubscriptions(sub2)
	h.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub2,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))

	h.operator.SetSubscriptions(sub2)
	h.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub1,
		EventType:    operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "a"))

	sub3 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub2", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub1", Topic: "c",
			Routes: subapi.Routes{Default: "/c"},
		},
	}
	h.operator.AddSubscriptions(sub3)
	h.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub3,
		EventType:    operatorv1.ResourceEventType_CREATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaSubscriptions(t, ctx), 2)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))

	sub2.Spec.Topic = "d"
	sub2.Spec.Routes.Default = "/d"
	h.operator.SetSubscriptions(sub2, sub3)
	h.operator.SubscriptionUpdateEvent(t, ctx, &api.SubscriptionUpdateEvent{
		Subscription: &sub2,
		EventType:    operatorv1.ResourceEventType_UPDATED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := h.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		if assert.Len(c, resp.GetSubscriptions(), 2) {
			assert.Equal(c, "c", resp.GetSubscriptions()[0].GetTopic())
			assert.Equal(c, "d", resp.GetSubscriptions()[1].GetTopic())
		}
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd, "pubsub0", "b"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))

	h.operator.SetComponents()
	h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{
		Component: &h.pubsub1,
		EventType: operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishError(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd, "pubsub1", "c"))

	h.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{
		Component: &h.pubsub2,
		EventType: operatorv1.ResourceEventType_DELETED,
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, h.daprd.GetMetaRegisteredComponents(t, ctx))
	}, time.Second*5, time.Millisecond*10)
	h.sub.ExpectPublishError(t, ctx, newReq(h.daprd, "pubsub0", "d"))
	h.sub.ExpectPublishError(t, ctx, newReq(h.daprd, "pubsub1", "c"))
}
