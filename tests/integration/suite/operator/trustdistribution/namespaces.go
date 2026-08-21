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
	suite.Register(new(namespaces))
}

// namespaces tests that a newly created namespace receives the trust anchors
// ConfigMap, and that namespace deletion does not wedge the operator.
type namespaces struct {
	sentry   *sentry.Sentry
	nsStore  *store.Store
	cmStore  *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (n *namespaces) Setup(t *testing.T) []framework.Option {
	n.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	n.nsStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	n.nsStore.Add(namespace("default"))
	n.cmStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})

	n.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			n.sentry.Port(),
		),
		kubernetes.WithClusterNamespaceListFromStore(t, n.nsStore),
		kubernetes.WithClusterConfigMapStore(t, n.cmStore),
	)

	n.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(n.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(n.sentry.TrustAnchorsFile(t)),
		operator.WithTrustDistribution(true),
	)

	return []framework.Option{
		framework.WithProcesses(n.kubeapi, n.sentry, n.operator),
	}
}

func (n *namespaces) Run(t *testing.T, ctx context.Context) {
	n.sentry.WaitUntilRunning(t, ctx)
	n.operator.WaitUntilRunning(t, ctx)

	waitForConfigMap := func(t *testing.T, ns string) {
		t.Helper()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, ok := n.cmStore.Get(ns, "dapr-root-ca.crt")
			assert.True(c, ok, "expected ConfigMap in namespace %q", ns)
		}, time.Second*20, time.Millisecond*100)
	}

	t.Run("existing namespace is reconciled", func(t *testing.T) {
		waitForConfigMap(t, "default")
	})

	t.Run("new namespace receives the ConfigMap", func(t *testing.T) {
		ns := namespace("frodo")
		n.nsStore.Add(ns)
		n.kubeapi.Informer().Add(t, ns)
		waitForConfigMap(t, "frodo")
	})

	t.Run("namespace deletion does not wedge the operator", func(t *testing.T) {
		ns := namespace("frodo")
		n.nsStore.Delete(ns)
		n.cmStore.Delete(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "frodo", Name: "dapr-root-ca.crt"}})
		n.kubeapi.Informer().Delete(t, ns)

		// The operator must still reconcile subsequently created namespaces.
		ns2 := namespace("samwise")
		n.nsStore.Add(ns2)
		n.kubeapi.Informer().Add(t, ns2)
		waitForConfigMap(t, "samwise")
	})
}
