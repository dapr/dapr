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
	suite.Register(new(fresh))
}

// fresh tests that the operator distributes the trust anchors ConfigMap into
// every existing namespace.
type fresh struct {
	sentry   *sentry.Sentry
	nsStore  *store.Store
	cmStore  *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (f *fresh) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	f.nsStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	f.nsStore.Add(namespace("dapr-system"), namespace("default"), namespace("foo"))
	f.cmStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})

	f.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			f.sentry.Port(),
		),
		kubernetes.WithClusterNamespaceListFromStore(t, f.nsStore),
		kubernetes.WithClusterConfigMapStore(t, f.cmStore),
	)

	f.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(f.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(f.sentry.TrustAnchorsFile(t)),
		operator.WithTrustDistribution(true),
	)

	return []framework.Option{
		framework.WithProcesses(f.kubeapi, f.sentry, f.operator),
	}
}

func (f *fresh) Run(t *testing.T, ctx context.Context) {
	f.sentry.WaitUntilRunning(t, ctx)
	f.operator.WaitUntilRunning(t, ctx)

	expAnchors := string(f.sentry.CABundle().X509.TrustAnchors)

	for _, ns := range []string{"dapr-system", "default", "foo"} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			obj, ok := f.cmStore.Get(ns, "dapr-root-ca.crt")
			if !assert.True(c, ok, "expected ConfigMap in namespace %q", ns) {
				return
			}
			cm, ok := obj.(*corev1.ConfigMap)
			if !assert.True(c, ok) {
				return
			}
			assert.Equal(c, map[string]string{"ca.crt": expAnchors}, cm.Data)
			assert.Equal(c, "dapr-operator", cm.Labels["app.kubernetes.io/managed-by"])
		}, time.Second*20, time.Millisecond*100)
	}
}
