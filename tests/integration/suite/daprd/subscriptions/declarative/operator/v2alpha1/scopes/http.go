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

package scopes

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	sub      *subscriber.Subscriber
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithRoutes(
		"/all", "/allempty", "/only1", "/only2", "/both",
	))

	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	appid1 := uuid.New().String()
	appid2 := uuid.New().String()

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
					ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "all",
						Routes: subapi.Routes{
							Default: "/all",
						},
					},
					Scopes: nil,
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub2", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "allempty",
						Routes: subapi.Routes{
							Default: "/allempty",
						},
					},
					Scopes: []string{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub3", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "only1",
						Routes: subapi.Routes{
							Default: "/only1",
						},
					},
					Scopes: []string{appid1},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub4", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "only2",
						Routes: subapi.Routes{
							Default: "/only2",
						},
					},
					Scopes: []string{appid2},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "sub5", Namespace: "default"},
					Spec: subapi.SubscriptionSpec{
						Pubsubname: "mypub",
						Topic:      "both",
						Routes: subapi.Routes{
							Default: "/both",
						},
					},
					Scopes: []string{appid1, appid2},
				},
			},
		}),
	)

	h.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(h.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	daprdOpts := []daprd.Option{
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
	}
	h.daprd1 = daprd.New(t, append(daprdOpts, daprd.WithAppID(appid1))...)
	h.daprd2 = daprd.New(t, append(daprdOpts, daprd.WithAppID(appid2))...)

	return []framework.Option{
		framework.WithProcesses(sentry, h.sub, h.kubeapi, h.operator, h.daprd1, h.daprd2),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.operator.WaitUntilRunning(t, ctx)
	h.daprd1.WaitUntilRunning(t, ctx)
	h.daprd2.WaitUntilRunning(t, ctx)

	client1 := h.daprd1.GRPCClient(t, ctx)
	client2 := h.daprd2.GRPCClient(t, ctx)

	meta, err := client1.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.ElementsMatch(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only1", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only1"}},
		}},
		{PubsubName: "mypub", Topic: "both", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	meta, err = client2.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.ElementsMatch(t, []*rtv1.PubsubSubscription{
		{PubsubName: "mypub", Topic: "all", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/all"}},
		}},
		{PubsubName: "mypub", Topic: "allempty", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/allempty"}},
		}},
		{PubsubName: "mypub", Topic: "only2", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/only2"}},
		}},
		{PubsubName: "mypub", Topic: "both", Type: rtv1.PubsubSubscriptionType_DECLARATIVE, Rules: &rtv1.PubsubSubscriptionRules{
			Rules: []*rtv1.PubsubSubscriptionRule{{Path: "/both"}},
		}},
	}, meta.GetSubscriptions())

	newReq := func(daprd *daprd.Daprd, topic string) subscriber.PublishRequest {
		return subscriber.PublishRequest{
			Daprd:      daprd,
			PubSubName: "mypub",
			Topic:      topic,
			Data:       `{"status": "completed"}`,
		}
	}

	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "all"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "allempty"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "only1"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd1, "only2"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd1, "both"))

	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "all"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "allempty"))
	h.sub.ExpectPublishNoReceive(t, ctx, newReq(h.daprd2, "only1"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "only2"))
	h.sub.ExpectPublishReceive(t, ctx, newReq(h.daprd2, "both"))

	h.sub.AssertEventChanLen(t, 0)
}
