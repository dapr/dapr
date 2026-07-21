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

package kubernetes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	sentryutils "github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(resume))
}

// resume ensures a sentry started against a trust-bundle Secret holding
// mid-rotation state resumes the persisted rotation instead of starting a new
// one, as happens when sentry restarts mid-rotation.
type resume struct {
	sentry        *procsentry.Sentry
	bndl          bundle.Bundle
	pending       *bundle.X509
	tb            sentryutils.TrustBundleRW
	distributedAt string
}

func (r *resume) Setup(t *testing.T) []framework.Option {
	r.bndl = genBundle(t, time.Hour*24)

	// The pending new CA, as a previous sentry's distributing phase would have
	// generated it.
	_, newRootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	r.pending, err = bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      newRootKey,
		TrustDomain:      trustDomain,
		AllowedClockSkew: time.Second * 5,
	})
	require.NoError(t, err)

	kubeAPI, tb := sentryutils.KubeAPIRW(t, sentryutils.KubeAPIOptions{
		Bundle:         r.bndl,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
	})
	r.tb = tb

	// Seed the Secret and ConfigMap with mid-rotation state: combined trust
	// anchors, the old issuer still signing, and a distributing phase begun an
	// hour ago.
	combined := make([]byte, 0, len(r.bndl.X509.TrustAnchors)+len(r.pending.TrustAnchors))
	combined = append(combined, r.bndl.X509.TrustAnchors...)
	combined = append(combined, r.pending.TrustAnchors...)
	oldRootNotAfter := certsFromPEM(t, r.bndl.X509.TrustAnchors)[0].NotAfter
	r.distributedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	secret := tb.Secret.Current(t)
	secret.Data["ca.crt"] = combined
	secret.Data[rotationPhaseKey] = []byte(bundle.RotationPhaseDistributing)
	secret.Data[rotationNewCACertKey] = r.pending.TrustAnchors
	secret.Data[rotationNewIssCertKey] = r.pending.IssChainPEM
	secret.Data[rotationNewIssKeyKey] = r.pending.IssKeyPEM
	secret.Data[rotationDistributedAtKey] = []byte(r.distributedAt)
	secret.Data[rotationOldRootNotAfterKey] = []byte(oldRootNotAfter.UTC().Format(time.RFC3339))
	tb.Secret.Set(t, secret)

	// The ConfigMap must stay in sync with the Secret's trust anchors,
	// otherwise sentry considers the bundle invalid and regenerates it.
	configMap := tb.ConfigMap.Current(t)
	configMap.Data["ca.crt"] = string(combined)
	tb.ConfigMap.Set(t, configMap)

	// The propagation window has long elapsed relative to the seeded
	// DistributedAt, so the resumed rotation must switch signing on the first
	// check.
	r.sentry = newSentry(t, kubeAPI, r.bndl,
		procsentry.WithRotationPropagationWindow(time.Second*2),
	)

	return []framework.Option{framework.WithProcesses(kubeAPI, r.sentry)}
}

func (r *resume) Run(t *testing.T, ctx context.Context) {
	r.sentry.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := r.tb.Secret.Current(t)
		assert.Equal(c, string(bundle.RotationPhaseSigning), string(secret.Data[rotationPhaseKey]))
	}, time.Second*20, time.Millisecond*100)

	secret := r.tb.Secret.Current(t)
	assert.Equal(t, r.distributedAt, string(secret.Data[rotationDistributedAtKey]),
		"sentry must resume the seeded rotation, not start a new one")

	// The seeded pending issuer must be the one promoted.
	issuer := certsFromPEM(t, secret.Data["issuer.crt"])[0]
	assert.True(t, issuer.Equal(r.pending.IssChain[0]), "the seeded pending issuer must be promoted")

	// Both root CAs must remain in the Secret and ConfigMap.
	oldRoot := certsFromPEM(t, r.bndl.X509.TrustAnchors)[0]
	newRoot := certsFromPEM(t, r.pending.TrustAnchors)[0]
	anchors := certsFromPEM(t, secret.Data["ca.crt"])
	require.Len(t, anchors, 2, "both root CAs must remain in the Secret trust anchors")
	assert.True(t, anchors[0].Equal(oldRoot))
	assert.True(t, anchors[1].Equal(newRoot))

	configMap := r.tb.ConfigMap.Current(t)
	cmAnchors := certsFromPEM(t, []byte(configMap.Data["ca.crt"]))
	require.Len(t, cmAnchors, 2, "both root CAs must remain in the ConfigMap trust anchors")
	assert.True(t, cmAnchors[0].Equal(oldRoot))
	assert.True(t, cmAnchors[1].Equal(newRoot))
}
