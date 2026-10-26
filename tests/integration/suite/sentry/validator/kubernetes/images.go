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
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/modes"
	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/tests/integration/suite/sentry/utils"
	secpem "github.com/dapr/kit/crypto/pem"
)

func init() {
	suite.Register(new(images))
}

// images tests that certificates issued via the Kubernetes validator carry the
// container images extension sourced from the requesting pod.
type images struct {
	sentry *sentry.Sentry
}

func (i *images) Setup(t *testing.T) []framework.Option {
	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    nil,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: "integration.test.dapr.io",
	})
	require.NoError(t, err)
	bundle := bundle.Bundle{
		X509: x509bundle,
		JWT:  jwtbundle,
	}

	kubeAPI := utils.KubeAPI(t, utils.KubeAPIOptions{
		Bundle:         bundle,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
		PodContainers: []corev1.Container{
			{Name: "daprd", Image: "ghcr.io/dapr/daprd:1.16.0"},
			{Name: "myapp", Image: "docker.io/library/myapp:v2"},
			{Name: "notstarted", Image: "docker.io/library/notstarted:v1"},
		},
		PodContainerStatuses: []corev1.ContainerStatus{
			{Name: "daprd", ImageID: "ghcr.io/dapr/daprd@sha256:aaa111"},
			{Name: "myapp", ImageID: "docker.io/library/myapp@sha256:bbb222"},
		},
	})

	i.sentry = sentry.New(t,
		sentry.WithWriteConfig(false),
		sentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		sentry.WithNamespace("sentrynamespace"),
		sentry.WithMode(string(modes.KubernetesMode)),
		sentry.WithExecOptions(
			// Enable Kubernetes validator.
			exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything"),
		),
		sentry.WithCABundle(bundle),
		sentry.WithTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(i.sentry, kubeAPI),
	}
}

func (i *images) Run(t *testing.T, ctx context.Context) {
	i.sentry.WaitUntilRunning(t, ctx)

	conn := i.sentry.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client := sentrypbv1.NewCAClient(conn)

	_, pk, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{ //nolint:gosec
		Id:                        "myappid",
		Namespace:                 "mynamespace",
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetWorkloadCertificate())

	certs, err := secpem.DecodePEMCertificates(resp.GetWorkloadCertificate())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	require.NoError(t, certs[0].CheckSignatureFrom(certs[1]))

	// The OID and wire format are hardcoded rather than referencing the
	// pkg/sentry/server/images constants, so an accidental change to either
	// fails this test instead of moving producer and assertion together.
	oidContainerImages := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57683, 100, 1}

	var found bool
	for _, ext := range certs[0].Extensions {
		if !ext.Id.Equal(oidContainerImages) {
			continue
		}
		found = true
		assert.False(t, ext.Critical, "container images extension must be non-critical")

		var jsonBytes []byte
		rest, err := asn1.Unmarshal(ext.Value, &jsonBytes)
		require.NoError(t, err)
		assert.Empty(t, rest)
		assert.JSONEq(t, `[
			{"role":"daprd","containerName":"daprd","image":"ghcr.io/dapr/daprd:1.16.0","digest":"sha256:aaa111"},
			{"role":"app","containerName":"myapp","image":"docker.io/library/myapp:v2","digest":"sha256:bbb222"},
			{"role":"app","containerName":"notstarted","image":"docker.io/library/notstarted:v1"}
		]`, string(jsonBytes))
	}
	require.True(t, found, "certificate should carry the container images extension")
	assert.Empty(t, certs[0].UnhandledCriticalExtensions)
}
