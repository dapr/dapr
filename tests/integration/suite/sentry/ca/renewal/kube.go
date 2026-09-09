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

package renewal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(kube))
}

// kube tests CA renewal in Kubernetes mode: the pending issuer pair is
// written into the trust bundle Secret, the appended anchors are mirrored to
// the ConfigMap, and the active issuer key is untouched.
type kube struct {
	sentry *procsentry.Sentry
	store  *prockube.TrustBundleStore
	bundle bundle.Bundle
}

func newKubeAPI(t *testing.T, store *prockube.TrustBundleStore) *prockube.Kubernetes {
	t.Helper()
	return prockube.New(t,
		prockube.WithClusterDaprConfigurationList(t, new(configapi.ConfigurationList)),
		prockube.WithClusterPodList(t, &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}}),
		prockube.WithDaprConfigurationGet(t, &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "daprsystem"},
			Spec: configapi.ConfigurationSpec{
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: "integration.test.dapr.io",
					AllowedClockSkew:        ptr.Of("5s"),
				},
			},
		}),
		prockube.WithTrustBundleStore(t, store),
	)
}

func (k *kube) Setup(t *testing.T) []framework.Option {
	k.bundle = genBundle(t, "integration.test.dapr.io", time.Second*65)

	k.store = prockube.NewTrustBundleStore(
		&corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			Data: map[string][]byte{
				"ca.crt":     k.bundle.X509.TrustAnchors,
				"issuer.crt": k.bundle.X509.IssChainPEM,
				"issuer.key": k.bundle.X509.IssKeyPEM,
			},
		},
		&corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			Data:       map[string]string{"ca.crt": string(k.bundle.X509.TrustAnchors)},
		},
	)
	kubeAPI := newKubeAPI(t, k.store)

	k.sentry = procsentry.New(t,
		procsentry.WithWriteConfig(false),
		procsentry.WithWriteTrustBundle(false),
		procsentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		procsentry.WithNamespace("sentrynamespace"),
		procsentry.WithMode(string(modes.KubernetesMode)),
		procsentry.WithExecOptions(exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything")),
		procsentry.WithCABundle(k.bundle),
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*55),
	)

	return []framework.Option{
		framework.WithProcesses(kubeAPI, k.sentry),
	}
}

func (k *kube) Run(t *testing.T, ctx context.Context) {
	k.sentry.WaitUntilRunning(t, ctx)

	t.Run("secret gains pending issuer pair and appended anchors", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			sec := k.store.Secret()
			assert.Contains(c, sec.Data, "issuer.next.crt")
			assert.Contains(c, sec.Data, "issuer.next.key")
			if !assert.Contains(c, sec.Data, "ca.crt") {
				return
			}
			assert.Len(c, parseAnchors(c, sec.Data["ca.crt"]), 2)
		}, time.Second*30, time.Millisecond*100)
	})

	sec := k.store.Secret()

	t.Run("old anchor and active issuer pair are untouched", func(t *testing.T) {
		anchors := sec.Data["ca.crt"]
		require.Greater(t, len(anchors), len(k.bundle.X509.TrustAnchors))
		assert.Equal(t, k.bundle.X509.TrustAnchors, anchors[:len(k.bundle.X509.TrustAnchors)])
		assert.Equal(t, k.bundle.X509.IssChainPEM, sec.Data["issuer.crt"])
		assert.Equal(t, k.bundle.X509.IssKeyPEM, sec.Data["issuer.key"])
	})

	t.Run("configmap mirrors the appended anchors", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			cm := k.store.ConfigMap()
			assert.Equal(c, string(sec.Data["ca.crt"]), cm.Data["ca.crt"])
		}, time.Second*10, time.Millisecond*100)
	})
}
