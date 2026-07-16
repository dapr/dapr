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

package match

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd    *daprd.Daprd
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	sub      *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithRoutes(
		"/aaa", "/type", "/foo", "/bar", "/topic", "/123", "/456", "/order7def", "/order7rule",
	))
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	h.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprComponentList(t, &compapi.ComponentList{
			Items: []compapi.Component{{
				ObjectMeta: metav1.ObjectMeta{Name: "mypub", Namespace: "default"},
				Spec: compapi.ComponentSpec{
					Type: "pubsub.in-memory", Version: "v1",
				},
			}},
		}),
		kubernetes.WithClusterDaprSubscriptionListV2(t, &subapi.SubscriptionList{
			Items: []subapi.Subscription{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "type", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "type",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/foo", Match: ""},
								{Path: "/bar", Match: `event.type == "com.dapr.event.recv"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order1", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order1",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
								{Path: "/topic", Match: `event.topic == "order1"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order2", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order2",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/topic", Match: `event.topic == "order2"`},
								{Path: "/type", Match: `event.type == "com.dapr.event.sent"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order3", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order3",
						Routes: subapi.Routes{
							Default: "/aaa",
							Rules: []subapi.Rule{
								{Path: "/123", Match: `event.topic == "order3"`},
								{Path: "/456", Match: `event.topic == "order3"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order4", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order4",
						Routes: subapi.Routes{
							Rules: []subapi.Rule{
								{Path: "/123", Match: `event.topic == "order5"`},
								{Path: "/456", Match: `event.topic == "order6"`},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "order7", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "order7",
						Routes: subapi.Routes{
							Default: "/order7def",
							Rules: []subapi.Rule{
								{Path: "/order7rule", Match: ""},
							},
						},
					},
				},
			},
		}),
	)

	h.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(h.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(h.operator.Address()),
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, h.sub, h.kubeapi, h.operator, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.operator.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)
	client := h.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "type",
	})
	require.NoError(t, err)
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order1",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order2",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/topic", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order3",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.Route)

	h.sub.ExpectPublishNoReceive(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "order4",
	})

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order7",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/order7rule", resp.Route)
}
