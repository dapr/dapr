/*
Copyright 2023 The Dapr Authors
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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	secpem "github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(kubernetes))
}

// kubernetes tests Sentry with the Kubernetes validator.
type kubernetes struct {
	sentry *sentry.Sentry
}

func (k *kubernetes) Setup(t *testing.T) []framework.Option {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	kubeAPI := kubeAPI(t, bundle, "mynamespace", "myserviceaccount")

	k.sentry = sentry.New(t,
		sentry.WithWriteConfig(false),
		sentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
		sentry.WithNamespace("sentrynamespace"),
		sentry.WithExecOptions(
			// Enable Kubernetes validator.
			exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything"),
		),
		sentry.WithCABundle(bundle),
		sentry.WithTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(k.sentry, kubeAPI),
	}
}

func (k *kubernetes) Run(t *testing.T, ctx context.Context) {
	k.sentry.WaitUntilRunning(t, ctx)

	conn := k.sentry.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client := sentrypbv1.NewCAClient(conn)

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
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
	require.Len(t, k.sentry.CABundle().IssChain, 1)
	assert.Equal(t, k.sentry.CABundle().IssChain[0].Raw, certs[1].Raw)
	trustBundle, err := secpem.DecodePEMCertificates(k.sentry.CABundle().TrustAnchors)
	require.NoError(t, err)
	require.Len(t, trustBundle, 1)
	require.NoError(t, certs[1].CheckSignatureFrom(trustBundle[0]))

	for _, req := range map[string]*sentrypbv1.SignCertificateRequest{
		"wrong app id": {
			Id:                        "notmyappid",
			Namespace:                 "mynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
			Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
		},
		"wrong namespace": {
			Id:                        "myappid",
			Namespace:                 "notmynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
			Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
		},
		"wrong token validator": {
			Id:                        "myappid",
			Namespace:                 "mynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_JWKS,
			Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
		},
		"wrong pod name": {
			Id:                        "myappid",
			Namespace:                 "mynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
			Token:                     `{"kubernetes.io":{"pod":{"name":"notmypod"}}}`,
		},
	} {
		_, err = client.SignCertificate(ctx, req)
		require.Error(t, err)
	}
}
