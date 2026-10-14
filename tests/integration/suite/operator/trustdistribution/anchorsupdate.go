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
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(anchorsupdate))
}

// anchorsupdate tests that appending a trust anchor to the operator's trust
// anchors file re-distributes the full appended bundle to the ConfigMap in
// every namespace without a restart. This is the propagation contract CA
// renewal relies on.
type anchorsupdate struct {
	sentry   *sentry.Sentry
	nsStore  *store.Store
	cmStore  *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
	taFile   string
}

func (a *anchorsupdate) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	a.taFile = filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(a.taFile, a.sentry.CABundle().X509.TrustAnchors, 0o600))

	a.nsStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	a.nsStore.Add(namespace("default"), namespace("foo"))
	a.cmStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})

	a.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			a.sentry.Port(),
		),
		kubernetes.WithClusterNamespaceListFromStore(t, a.nsStore),
		kubernetes.WithClusterConfigMapStore(t, a.cmStore),
	)

	a.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(a.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(a.taFile),
		operator.WithTrustDistribution(true),
	)

	return []framework.Option{
		framework.WithProcesses(a.kubeapi, a.sentry, a.operator),
	}
}

func (a *anchorsupdate) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.operator.WaitUntilRunning(t, ctx)

	original := string(a.sentry.CABundle().X509.TrustAnchors)

	waitForAnchors := func(t *testing.T, exp string) {
		t.Helper()
		for _, ns := range []string{"default", "foo"} {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				obj, ok := a.cmStore.Get(ns, "dapr-root-ca.crt")
				if !assert.True(c, ok, "expected ConfigMap in namespace %q", ns) {
					return
				}
				cm, ok := obj.(*corev1.ConfigMap)
				if !assert.True(c, ok) {
					return
				}
				assert.Equal(c, exp, cm.Data["ca.crt"])
			}, time.Second*20, time.Millisecond*100)
		}
	}

	t.Run("initial distribution", func(t *testing.T) {
		waitForAnchors(t, original)
	})

	t.Run("appended trust anchor is re-distributed", func(t *testing.T) {
		_, rootKey, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		newBundle, err := bundle.GenerateX509(bundle.OptionsX509{
			X509RootKey:      rootKey,
			TrustDomain:      "integration.test.dapr.io",
			AllowedClockSkew: time.Second * 5,
		})
		require.NoError(t, err)

		combined := original + string(newBundle.TrustAnchors)
		require.NoError(t, os.WriteFile(a.taFile, []byte(combined), 0o600))

		waitForAnchors(t, combined)
	})
}
