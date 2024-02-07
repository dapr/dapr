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

package operator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	suite.Register(new(informer))
}

// informer tests operator hot reloading with a live operator and daprd, using
// the process kubernetes informer.
type informer struct {
	daprd    *daprd.Daprd
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (i *informer) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	i.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Component",
	})
	i.kubeapi = kubernetes.New(t,
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
		kubernetes.WithClusterDaprComponentListFromStore(t, i.store),
	)

	i.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(i.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	i.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(i.operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, i.kubeapi, i.operator, i.daprd),
	}
}

func (i *informer) Run(t *testing.T, ctx context.Context) {
	i.operator.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		assert.Empty(t, util.GetMetaComponents(t, ctx, client, i.daprd.HTTPPort()))
	})

	t.Run("adding a component should become available", func(t *testing.T) {
		comp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
			ObjectMeta: metav1.ObjectMeta{Name: "123", Namespace: "default"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		i.store.Add(&comp)
		i.kubeapi.Informer().Add(t, &comp)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(t, ctx, client, i.daprd.HTTPPort()), 1)
		}, time.Second*10, time.Millisecond*100)
		metaComponents := util.GetMetaComponents(t, ctx, client, i.daprd.HTTPPort())
		assert.ElementsMatch(t, metaComponents, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		})
	})

	dir := filepath.Join(t.TempDir(), "db.sqlite")
	dirJSON, err := json.Marshal(dir)
	require.NoError(t, err)
	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "abc", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type:    "state.sqlite",
			Version: "v1",
			Metadata: []common.NameValuePair{
				{Name: "connectionString", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: dirJSON},
				}},
			},
		},
	}

	t.Run("updating component should be updated", func(t *testing.T) {
		i.store.Set(&comp)
		i.kubeapi.Informer().Modify(t, &comp)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.ElementsMatch(c, util.GetMetaComponents(t, ctx, client, i.daprd.HTTPPort()), []*rtv1.RegisteredComponents{
				{
					Name: "abc", Type: "state.sqlite", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "ACTOR"},
				},
			})
		}, time.Second*10, time.Millisecond*100)
	})

	t.Run("deleting a component should delete the component", func(t *testing.T) {
		i.store.Set()
		i.kubeapi.Informer().Delete(t, &comp)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, i.daprd.HTTPPort()))
		}, time.Second*20, time.Millisecond*100)
	})
}
