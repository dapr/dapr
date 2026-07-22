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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	sentryutils "github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(distributing))
}

// distributing ensures a near-expiry root CA starts a rotation which persists
// its state in the trust-bundle Secret and appends both root CAs to the
// trust-bundle ConfigMap, while signing still uses the old issuer.
type distributing struct {
	sentry *procsentry.Sentry
	bndl   bundle.Bundle
	tb     sentryutils.TrustBundleRW
}

func (d *distributing) Setup(t *testing.T) []framework.Option {
	// The root CA expires within the default 30 day trigger window so rotation
	// starts on sentry's first check. The default 24h propagation window keeps
	// the rotation parked in the distributing phase.
	d.bndl = cert.GenerateCABundle(t, trustDomain, time.Hour*24)

	kubeAPI, tb := sentryutils.KubeAPIRW(t, sentryutils.KubeAPIOptions{
		Bundle:         d.bndl,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
	})
	d.tb = tb
	d.sentry = procsentry.New(t,
		procsentry.WithKubeAPI(t, kubeAPI, "sentrynamespace"),
		procsentry.WithCABundle(d.bndl),
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithRotationEnabled(true),
		procsentry.WithRotationCheckInterval(time.Second),
	)

	return []framework.Option{framework.WithProcesses(kubeAPI, d.sentry)}
}

func (d *distributing) Run(t *testing.T, ctx context.Context) {
	d.sentry.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := d.tb.Secret.Current(t)
		assert.Equal(c, string(bundle.RotationPhaseDistributing), string(secret.Data[procsentry.RotationPhaseSecretKey]))
	}, time.Second*20, time.Millisecond*10)

	secret := d.tb.Secret.Current(t)
	for _, key := range []string{
		procsentry.RotationNewCACertSecretKey,
		procsentry.RotationNewIssCertSecretKey,
		procsentry.RotationNewIssKeySecretKey,
		procsentry.RotationDistributedAtSecretKey,
		procsentry.RotationOldRootNotAfterSecretKey,
	} {
		assert.NotEmpty(t, secret.Data[key], "rotation state key %q must be persisted", key)
	}

	oldRoot := cert.DecodePEM(t, d.bndl.X509.TrustAnchors)[0]
	newRoot := cert.DecodePEM(t, secret.Data[procsentry.RotationNewCACertSecretKey])[0]
	assert.False(t, newRoot.Equal(oldRoot))

	// The Secret trust anchors must contain the old and new root CAs,
	// appended.
	anchors := cert.DecodePEM(t, secret.Data["ca.crt"])
	require.Len(t, anchors, 2, "trust anchors must contain both root CAs")
	assert.True(t, anchors[0].Equal(oldRoot))
	assert.True(t, anchors[1].Equal(newRoot))

	// The ConfigMap — mounted by workloads as their trust anchor source — must
	// carry both root CAs so pods begin trusting the new root before signing
	// switches.
	configMap := d.tb.ConfigMap.Current(t)
	cmAnchors := cert.DecodePEM(t, []byte(configMap.Data["ca.crt"]))
	require.Len(t, cmAnchors, 2, "ConfigMap trust anchors must contain both root CAs")
	assert.True(t, cmAnchors[0].Equal(oldRoot))
	assert.True(t, cmAnchors[1].Equal(newRoot))

	// Signing must still use the old issuer during distribution.
	issuer := cert.DecodePEM(t, secret.Data["issuer.crt"])[0]
	assert.True(t, issuer.Equal(d.bndl.X509.IssChain[0]), "signing must still use the old issuer")
}
