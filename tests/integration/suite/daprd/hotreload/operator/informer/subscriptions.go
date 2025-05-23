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

package informer

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(subscriptions))
}

type subscriptions struct {
	sub       *subscriber.Subscriber
	daprd     *daprd.Daprd
	compStore *store.Store
	subStore  *store.Store
	kubeapi   *kubernetes.Kubernetes
	operator  *operator.Operator
}

func (s *subscriptions) Setup(t *testing.T) []framework.Option {
	s.sub = subscriber.New(t)
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	s.compStore = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Component",
	})
	s.subStore = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v2alpha1",
		Kind:    "Subscription",
	})

	s.compStore.Add(&compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "pubsub0", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	})

	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "daprsystem"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sentry.Address(),
					},
					Features: []configapi.FeatureSpec{{
						Name:    "HotReload",
						Enabled: ptr.Of(true),
					}},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentListFromStore(t, s.compStore),
		kubernetes.WithClusterDaprSubscriptionV2ListFromStore(t, s.subStore),
	)

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(s.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.sub, s.kubeapi, s.operator, s.daprd),
	}
}

func (s *subscriptions) Run(t *testing.T, ctx context.Context) {
	s.operator.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, s.daprd.GetMetaRegisteredComponents(t, ctx), 1)

	newReq := func(pubsub, topic string) *rtv1.PublishEventRequest {
		return &rtv1.PublishEventRequest{PubsubName: pubsub, Topic: topic, Data: []byte(`{"status": "completed"}`)}
	}
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))

	sub1 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub0", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	s.subStore.Add(&sub1)
	s.kubeapi.Informer().Add(t, &sub1)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))

	sub2 := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub1", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "pubsub0", Topic: "b",
			Routes: subapi.Routes{Default: "/b"},
		},
	}
	s.subStore.Add(&sub2)
	s.kubeapi.Informer().Add(t, &sub2)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 2)
	}, time.Second*10, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))

	sub2.Spec.Topic = "c"
	s.subStore.Set(&sub1, &sub2)
	s.kubeapi.Informer().Modify(t, &sub2)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := s.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		if assert.Len(c, resp.GetSubscriptions(), 2) {
			assert.Equal(c, "a", resp.GetSubscriptions()[0].GetTopic())
			assert.Equal(c, "c", resp.GetSubscriptions()[1].GetTopic())
		}
	}, time.Second*15, time.Millisecond*10)
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "c"))

	s.subStore.Set(&sub1)
	s.kubeapi.Informer().Delete(t, &sub2)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*25, time.Millisecond*10)
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "c"))
	s.sub.ExpectPublishNoReceive(t, ctx, s.daprd, newReq("pubsub0", "b"))
	s.sub.ExpectPublishReceive(t, ctx, s.daprd, newReq("pubsub0", "a"))
}
