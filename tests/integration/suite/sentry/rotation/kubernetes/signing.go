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
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	sentryutils "github.com/dapr/dapr/tests/integration/suite/sentry/utils"
)

func init() {
	suite.Register(new(signing))
}

// signing ensures that once the propagation window has elapsed, signing
// switches to the new issuer in the trust-bundle Secret while both root CAs
// remain in the Secret and ConfigMap.
type signing struct {
	sentry *procsentry.Sentry
	bndl   bundle.Bundle
	tb     sentryutils.TrustBundleRW
}

func (s *signing) Setup(t *testing.T) []framework.Option {
	// Near-expiry root CA triggers rotation immediately; the short propagation
	// window lets the rotation progress to the signing phase, where it stays as
	// the old root CA is still valid for the duration of the test.
	s.bndl = genBundle(t, time.Hour)

	kubeAPI, tb := sentryutils.KubeAPIRW(t, sentryutils.KubeAPIOptions{
		Bundle:         s.bndl,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
	})
	s.tb = tb
	s.sentry = newSentry(t, kubeAPI, s.bndl,
		procsentry.WithRotationPropagationWindow(time.Second*2),
	)

	return []framework.Option{framework.WithProcesses(kubeAPI, s.sentry)}
}

func (s *signing) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		secret := s.tb.Secret.Current(t)
		assert.Equal(c, string(bundle.RotationPhaseSigning), string(secret.Data[rotationPhaseKey]))
	}, time.Second*30, time.Millisecond*100)

	secret := s.tb.Secret.Current(t)
	assert.NotEmpty(t, secret.Data[rotationSigningAtKey], "SigningAt must be recorded")

	oldRoot := certsFromPEM(t, s.bndl.X509.TrustAnchors)[0]
	oldIssuer := s.bndl.X509.IssChain[0]
	newRoot := certsFromPEM(t, secret.Data[rotationNewCACertKey])[0]
	newIssuer := certsFromPEM(t, secret.Data[rotationNewIssCertKey])[0]

	// The pending issuer must be promoted to the active issuer.
	issuer := certsFromPEM(t, secret.Data["issuer.crt"])[0]
	assert.True(t, issuer.Equal(newIssuer), "the pending issuer must be promoted to the active issuer")
	assert.False(t, issuer.Equal(oldIssuer))
	require.NoError(t, issuer.CheckSignatureFrom(newRoot))

	// Both root CAs must remain in the Secret and the ConfigMap so workload
	// certs signed by the old issuer keep verifying until they expire.
	anchors := certsFromPEM(t, secret.Data["ca.crt"])
	require.Len(t, anchors, 2, "both root CAs must remain in the Secret trust anchors")
	assert.True(t, anchors[0].Equal(oldRoot))
	assert.True(t, anchors[1].Equal(newRoot))

	configMap := s.tb.ConfigMap.Current(t)
	cmAnchors := certsFromPEM(t, []byte(configMap.Data["ca.crt"]))
	require.Len(t, cmAnchors, 2, "both root CAs must remain in the ConfigMap trust anchors")
	assert.True(t, cmAnchors[0].Equal(oldRoot))
	assert.True(t, cmAnchors[1].Equal(newRoot))
}
