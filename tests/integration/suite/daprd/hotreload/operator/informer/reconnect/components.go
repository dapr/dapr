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
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(components))
}

type components struct {
	daprd     *daprd.Daprd
	store     *store.Store
	kubeapi   *kubernetes.Kubernetes
	operator1 *operator.Operator
	operator2 *operator.Operator
	operator3 *operator.Operator
}

func (c *components) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	c.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Component",
	})
	c.kubeapi = kubernetes.New(t,
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
		kubernetes.WithClusterDaprComponentListFromStore(t, c.store),
	)

	opts := []operator.Option{
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(c.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	}
	c.operator1 = operator.New(t, opts...)
	c.operator2 = operator.New(t, append(opts,
		operator.WithAPIPort(c.operator1.Port()),
	)...)
	c.operator3 = operator.New(t, append(opts,
		operator.WithAPIPort(c.operator1.Port()),
	)...)

	c.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(c.operator1.Address()),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, c.kubeapi, c.daprd),
	}
}

func (c *components) Run(t *testing.T, ctx context.Context) {
	c.operator1.Run(t, ctx)
	c.operator1.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "123", Namespace: "default"},
		Spec:       compapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
	}
	c.store.Add(&comp)
	c.kubeapi.Informer().Add(t, &comp)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, util.GetMetaComponents(t, ctx, client, c.daprd.HTTPPort()), 1)
	}, time.Second*10, time.Millisecond*10)

	c.operator1.Cleanup(t)
	c.store.Set()
	c.kubeapi.Informer().Delete(t, &comp)
	c.operator2.Run(t, ctx)
	c.operator2.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Empty(t, util.GetMetaComponents(t, ctx, client, c.daprd.HTTPPort()))
	}, time.Second*10, time.Millisecond*10)

	c.operator2.Cleanup(t)
	c.store.Add(&comp)
	c.kubeapi.Informer().Add(t, &comp)
	c.operator3.Run(t, ctx)
	c.operator3.WaitUntilRunning(t, ctx)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Empty(t, util.GetMetaComponents(t, ctx, client, c.daprd.HTTPPort()))
	}, time.Second*10, time.Millisecond*10)

	c.operator3.Cleanup(t)
}
