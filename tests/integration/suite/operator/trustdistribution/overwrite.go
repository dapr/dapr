/*
Copyright 2026 The Dapr Authors
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

package trustdistribution

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(overwrite))
}

// overwrite tests that the operator restores the distributed trust anchors
// ConfigMap when it is mutated or deleted by a third party.
type overwrite struct {
	sentry   *sentry.Sentry
	nsStore  *store.Store
	cmStore  *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	o.nsStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	o.nsStore.Add(namespace("default"))
	o.cmStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})

	o.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			o.sentry.Port(),
		),
		kubernetes.WithClusterNamespaceListFromStore(t, o.nsStore),
		kubernetes.WithClusterConfigMapStore(t, o.cmStore),
	)

	o.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(o.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(o.sentry.TrustAnchorsFile(t)),
		operator.WithTrustDistribution(true),
	)

	return []framework.Option{
		framework.WithProcesses(o.kubeapi, o.sentry, o.operator),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)
	o.operator.WaitUntilRunning(t, ctx)

	expAnchors := string(o.sentry.CABundle().X509.TrustAnchors)

	waitForRestore := func(t *testing.T) {
		t.Helper()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			obj, ok := o.cmStore.Get("default", "dapr-root-ca.crt")
			if !assert.True(c, ok) {
				return
			}
			cm, ok := obj.(*corev1.ConfigMap)
			if !assert.True(c, ok) {
				return
			}
			assert.Equal(c, map[string]string{"ca.crt": expAnchors}, cm.Data)
		}, time.Second*20, time.Millisecond*100)
	}

	t.Run("initial distribution", func(t *testing.T) {
		waitForRestore(t)
	})

	t.Run("mutated ConfigMap is restored", func(t *testing.T) {
		obj, ok := o.cmStore.Get("default", "dapr-root-ca.crt")
		require.True(t, ok)
		cm, ok := obj.(*corev1.ConfigMap)
		require.True(t, ok)

		mutated := cm.DeepCopy()
		mutated.Data = map[string]string{"ca.crt": "rogue", "extra": "key"}
		o.cmStore.Add(mutated)
		o.kubeapi.Informer().Modify(t, mutated)

		waitForRestore(t)
	})

	t.Run("deleted ConfigMap is recreated", func(t *testing.T) {
		obj, ok := o.cmStore.Get("default", "dapr-root-ca.crt")
		require.True(t, ok)
		cm, ok := obj.(*corev1.ConfigMap)
		require.True(t, ok)

		o.cmStore.Delete(cm)
		o.kubeapi.Informer().Delete(t, cm)

		waitForRestore(t)
	})
}
