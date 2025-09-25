/*
Copyright 2024 The Dapr Authors
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

package reconnect

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
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
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
	daprd     *daprd.Daprd
	compStore *store.Store
	subStore  *store.Store
	kubeapi   *kubernetes.Kubernetes
	operator1 *operator.Operator
	operator2 *operator.Operator
	operator3 *operator.Operator
}

func (s *subscriptions) Setup(t *testing.T) []framework.Option {
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

	opts := []operator.Option{
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	}
	s.operator1 = operator.New(t, opts...)
	s.operator2 = operator.New(t, append(opts,
		operator.WithAPIPort(s.operator1.Port()),
	)...)
	s.operator3 = operator.New(t, append(opts,
		operator.WithAPIPort(s.operator1.Port()),
	)...)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator1.Address()),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.kubeapi, s.daprd),
	}
}

func (s *subscriptions) Run(t *testing.T, ctx context.Context) {
	s.operator1.Run(t, ctx)
	s.operator1.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "123", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "pubsub.in-memory", Version: "v1"},
	}
	sub := subapi.Subscription{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "Subscription"},
		ObjectMeta: metav1.ObjectMeta{Name: "sub0", Namespace: "default"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "123", Topic: "a",
			Routes: subapi.Routes{Default: "/a"},
		},
	}
	s.compStore.Add(&comp)
	s.subStore.Add(&sub)
	s.kubeapi.Informer().Add(t, &comp)
	s.kubeapi.Informer().Add(t, &sub)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	s.operator1.Cleanup(t)
	s.subStore.Set()
	s.kubeapi.Informer().Delete(t, &sub)
	s.operator2.Run(t, ctx)
	s.operator2.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		assert.Empty(c, s.daprd.GetMetaSubscriptions(c, ctx))
	}, time.Second*10, time.Millisecond*10)

	s.operator2.Cleanup(t)
	s.subStore.Add(&sub)
	s.kubeapi.Informer().Add(t, &sub)
	s.operator3.Run(t, ctx)
	s.operator3.WaitUntilRunning(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		assert.Len(c, s.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	s.operator3.Cleanup(t)
}
