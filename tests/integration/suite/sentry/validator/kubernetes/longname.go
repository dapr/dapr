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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(longname))
}

// longname tests that sentry with _not_ authenticate requests with legacy
// identities that use namespace + serviceaccount names longer than 253
// characters, or app IDs longer than 64 characters.
type longname struct {
	sentry1 *sentry.Sentry
	sentry2 *sentry.Sentry
	sentry3 *sentry.Sentry
}

func (l *longname) Setup(t *testing.T) []framework.Option {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	kubeAPI1 := kubeAPI(t, kubeAPIOptions{
		bundle:         bundle,
		namespace:      strings.Repeat("n", 253),
		serviceAccount: strings.Repeat("s", 253),
		appID:          "myapp",
	})

	kubeAPI2 := kubeAPI(t, kubeAPIOptions{
		bundle:         bundle,
		namespace:      strings.Repeat("n", 253),
		serviceAccount: strings.Repeat("s", 253),
		appID:          strings.Repeat("a", 65),
	})

	kubeAPI3 := kubeAPI(t, kubeAPIOptions{
		bundle:         bundle,
		namespace:      strings.Repeat("n", 253),
		serviceAccount: strings.Repeat("s", 253),
		appID:          strings.Repeat("a", 64),
	})

	sentryOpts := func(kubeAPI *kubernetes.Kubernetes) *sentry.Sentry {
		return sentry.New(t,
			sentry.WithWriteConfig(false),
			sentry.WithKubeconfig(kubeAPI.KubeconfigPath(t)),
			sentry.WithExecOptions(
				// Enable Kubernetes validator.
				exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything"),
				exec.WithEnvVars(t, "NAMESPACE", "sentrynamespace"),
			),
			sentry.WithCABundle(bundle),
			sentry.WithTrustDomain("integration.test.dapr.io"),
		)
	}

	l.sentry1 = sentryOpts(kubeAPI1)
	l.sentry2 = sentryOpts(kubeAPI2)
	l.sentry3 = sentryOpts(kubeAPI3)

	return []framework.Option{
		framework.WithProcesses(kubeAPI1, kubeAPI2, kubeAPI3, l.sentry1, l.sentry2, l.sentry3),
	}
}

func (l *longname) Run(t *testing.T, ctx context.Context) {
	l.sentry1.WaitUntilRunning(t, ctx)
	l.sentry2.WaitUntilRunning(t, ctx)
	l.sentry3.WaitUntilRunning(t, ctx)

	conn1 := l.sentry1.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client1 := sentrypbv1.NewCAClient(conn1)

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	resp, err := client1.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
		Id:                        strings.Repeat("n", 253) + ":" + strings.Repeat("s", 253),
		Namespace:                 strings.Repeat("n", 253),
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	assert.Nil(t, resp)
	require.ErrorContains(t, err, "app ID must be 64 characters or less")
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	conn2 := l.sentry2.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client2 := sentrypbv1.NewCAClient(conn2)

	resp, err = client2.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
		Id:                        strings.Repeat("a", 65),
		Namespace:                 strings.Repeat("n", 253),
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	assert.Nil(t, resp)
	require.ErrorContains(t, err, "app ID must be 64 characters or less")
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	conn3 := l.sentry3.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client3 := sentrypbv1.NewCAClient(conn3)

	resp, err = client3.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
		Id:                        strings.Repeat("a", 64),
		Namespace:                 strings.Repeat("n", 253),
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetWorkloadCertificate())
}
