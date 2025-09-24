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
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	sub      *subscriber.Subscriber
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))
	g.sub = subscriber.New(t)

	appid1 := uuid.New().String()
	appid2 := uuid.New().String()

	g.kubeapi = kubernetes.New(t,
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

	g.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(g.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	daprdOpts := []daprd.Option{
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(g.operator.Address()),
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	}
	g.daprd1 = daprd.New(t, append(daprdOpts, daprd.WithAppID(appid1))...)
	g.daprd2 = daprd.New(t, append(daprdOpts, daprd.WithAppID(appid2))...)

	return []framework.Option{
		framework.WithProcesses(sentry, g.sub, g.kubeapi, g.operator, g.daprd1, g.daprd2),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.operator.WaitUntilRunning(t, ctx)
	g.daprd1.WaitUntilRunning(t, ctx)
	g.daprd2.WaitUntilRunning(t, ctx)

	client1 := g.daprd1.GRPCClient(t, ctx)
	client2 := g.daprd2.GRPCClient(t, ctx)

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

	newReq := func(topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: "mypub", Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}

	reqAll := newReq("all")
	reqEmpty := newReq("allempty")
	reqOnly1 := newReq("only1")
	reqOnly2 := newReq("only2")
	reqBoth := newReq("both")

	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqAll)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqEmpty)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqOnly1)
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd1, reqOnly2)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd1, reqBoth)

	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqAll)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqEmpty)
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd2, reqOnly1)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqOnly2)
	g.sub.ExpectPublishReceive(t, ctx, g.daprd2, reqBoth)

	g.sub.AssertEventChanLen(t, 0)
}
