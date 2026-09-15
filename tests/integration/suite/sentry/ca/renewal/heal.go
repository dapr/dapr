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
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(heal))
}

// heal is a regression test for the trust bundle ConfigMap sync check: a
// ConfigMap which is out of sync with the Secret (here stale after a
// simulated crash between the Secret and ConfigMap updates during renewal)
// must be healed from the Secret, NOT trigger a full CA regeneration.
type heal struct {
	sentry  *procsentry.Sentry
	store   *prockube.TrustBundleStore
	renewed *bundle.X509
}

func (h *heal) Setup(t *testing.T) []framework.Option {
	// A valid renewed bundle: two anchors, active pair, pending pair.
	base := genBundle(t, "integration.test.dapr.io", time.Hour*24*365)
	_, newRootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	h.renewed, err = bundle.RenewX509(bundle.OptionsRenewX509{
		Existing:         base.X509,
		X509RootKey:      newRootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 5,
	})
	require.NoError(t, err)

	h.store = prockube.NewTrustBundleStore(
		&corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			Data: map[string][]byte{
				"ca.crt":          h.renewed.TrustAnchors,
				"issuer.crt":      h.renewed.IssChainPEM,
				"issuer.key":      h.renewed.IssKeyPEM,
				"issuer.next.crt": h.renewed.NextIssChainPEM,
				"issuer.next.key": h.renewed.NextIssKeyPEM,
			},
		},
		&corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			// Stale: only the old anchor, as if sentry crashed between the
			// Secret and ConfigMap updates.
			Data: map[string]string{"ca.crt": string(base.X509.TrustAnchors)},
		},
	)
	kubeAPI := newKubeAPI(t, h.store)

	h.sentry = procsentry.New(t,
		procsentry.WithWriteConfig(false),
		procsentry.WithWriteTrustBundle(false),
		procsentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		procsentry.WithNamespace("sentrynamespace"),
		procsentry.WithMode(string(modes.KubernetesMode)),
		procsentry.WithExecOptions(exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything")),
		procsentry.WithCABundle(bundle.Bundle{X509: h.renewed}),
		procsentry.WithTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(kubeAPI, h.sentry),
	}
}

func (h *heal) Run(t *testing.T, ctx context.Context) {
	h.sentry.WaitUntilRunning(t, ctx)

	t.Run("configmap is healed from the secret", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			cm := h.store.ConfigMap()
			assert.Equal(c, string(h.renewed.TrustAnchors), cm.Data["ca.crt"])
		}, time.Second*20, time.Millisecond*100)
	})

	t.Run("the CA was not regenerated", func(t *testing.T) {
		sec := h.store.Secret()
		assert.Equal(t, h.renewed.TrustAnchors, sec.Data["ca.crt"])
		assert.Equal(t, h.renewed.IssChainPEM, sec.Data["issuer.crt"])
		assert.Equal(t, h.renewed.IssKeyPEM, sec.Data["issuer.key"], "issuer key must be untouched")
		assert.Equal(t, h.renewed.NextIssChainPEM, sec.Data["issuer.next.crt"])
		assert.Equal(t, h.renewed.NextIssKeyPEM, sec.Data["issuer.next.key"])
	})
}
