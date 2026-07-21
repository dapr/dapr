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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	secpem "github.com/dapr/kit/crypto/pem"
)

const trustDomain = "integration.test.dapr.io"

// Rotation state keys persisted by sentry in the dapr-trust-bundle Secret.
const (
	rotationPhaseKey           = "rotation.phase"
	rotationNewCACertKey       = "rotation.new-ca.crt"
	rotationNewIssCertKey      = "rotation.new-issuer.crt"
	rotationNewIssKeyKey       = "rotation.new-issuer.key"
	rotationDistributedAtKey   = "rotation.distributed-at"
	rotationSigningAtKey       = "rotation.signing-at"
	rotationOldRootNotAfterKey = "rotation.old-root-not-after"
)

var rotationKeys = []string{
	rotationPhaseKey,
	rotationNewCACertKey,
	rotationNewIssCertKey,
	rotationNewIssKeyKey,
	rotationDistributedAtKey,
	rotationSigningAtKey,
	rotationOldRootNotAfterKey,
}

// genBundle generates a CA bundle whose root CA and issuer cert expire after
// ttl.
func genBundle(t *testing.T, ttl time.Duration) bundle.Bundle {
	t.Helper()

	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      trustDomain,
		AllowedClockSkew: time.Second * 5,
		OverrideCATTL:    &ttl,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: trustDomain,
	})
	require.NoError(t, err)

	return bundle.Bundle{X509: x509bundle, JWT: jwtbundle}
}

// certsFromPEM decodes all certificates from PEM data.
func certsFromPEM(t *testing.T, data []byte) []*x509.Certificate {
	t.Helper()
	certs, err := secpem.DecodePEMCertificates(data)
	require.NoError(t, err)
	require.NotEmpty(t, certs)
	return certs
}

// newSentry returns a sentry process configured for Kubernetes mode against
// the given mock API server, with a 1s rotation check interval.
func newSentry(t *testing.T, kubeAPI *prockube.Kubernetes, bndle bundle.Bundle, opts ...procsentry.Option) *procsentry.Sentry {
	t.Helper()

	return procsentry.New(t, append([]procsentry.Option{
		procsentry.WithWriteConfig(false),
		procsentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		procsentry.WithNamespace("sentrynamespace"),
		procsentry.WithMode(string(modes.KubernetesMode)),
		procsentry.WithExecOptions(exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything")),
		procsentry.WithCABundle(bndle),
		procsentry.WithTrustDomain(trustDomain),
		procsentry.WithRotationCheckInterval(time.Second),
	}, opts...)...)
}
