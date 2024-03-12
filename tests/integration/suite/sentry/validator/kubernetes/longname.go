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

	"github.com/stretchr/testify/require"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(longname))
}

// longname tests that sentry with authenticate requests with legacy identities
// that use namespace + serviceaccount names longer than 253 characters.
type longname struct {
	sentry *sentry.Sentry
}

func (l *longname) Setup(t *testing.T) []framework.Option {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	kubeAPI := kubeAPI(t, bundle, strings.Repeat("n", 253), strings.Repeat("s", 253))

	l.sentry = sentry.New(t,
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
		framework.WithProcesses(l.sentry, kubeAPI),
	}
}

func (l *longname) Run(t *testing.T, ctx context.Context) {
	l.sentry.WaitUntilRunning(t, ctx)

	conn := l.sentry.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client := sentrypbv1.NewCAClient(conn)

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
		Id:                        strings.Repeat("n", 253) + ":" + strings.Repeat("s", 253),
		Namespace:                 strings.Repeat("n", 253),
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_KUBERNETES,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetWorkloadCertificate())
}
