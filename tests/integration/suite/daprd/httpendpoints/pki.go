/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package httpenedpoints

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	cryptotest "github.com/dapr/kit/crypto/test"
)

func init() {
	suite.Register(new(pki))
}

type pki struct {
	daprd *daprd.Daprd
}

func (p *pki) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	certs := cryptotest.GenPKI(t, cryptotest.PKIOptions{LeafDNS: "localhost"})

	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Hello, World!"))
		}),
		prochttp.WithTLS(t, certs.LeafCertPEM, certs.LeafPKPEM),
	)

	app := app.New(t)

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithSecretList(t, &corev1.SecretList{
			Items: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dapr-tls-certificates",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"tls.crt": certs.LeafCertPEM,
						"tls.key": certs.LeafPKPEM,
					},
				},
			},
		}),
		kubernetes.WithClusterDaprHTTPEndpointList(t, &httpendpointapi.HTTPEndpointList{
			Items: []httpendpointapi.HTTPEndpoint{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foobar",
						Namespace: "default",
					},
					Spec: httpendpointapi.HTTPEndpointSpec{
						BaseURL: "https://localhost:" + strconv.Itoa(srv.Port()),
						ClientTLS: &common.TLS{
							Certificate: &common.TLSDocument{
								SecretKeyRef: &common.SecretKeyRef{
									Name: "dapr-tls-certificates",
									Key:  "tls.crt",
								},
							},
							PrivateKey: &common.TLSDocument{
								SecretKeyRef: &common.SecretKeyRef{
									Name: "dapr-tls-certificates",
									Key:  "tls.key",
								},
							},
						},
					},
				},
			},
		}),
	)

	operator := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	p.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
		daprd.WithAppPort(app.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, app, sentry, kubeapi, operator, p.daprd),
	}
}

func (p *pki) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, p.daprd.GetMetaHTTPEndpoints(t, ctx), 1)
	}, time.Second*5, time.Millisecond*10)

	client := client.HTTP(t)

	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s/v1.0/invoke/foobar/method/hello", p.daprd.HTTPAddress()),
		nil,
	)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Contains(t, string(body), "tls: failed to verify certificate")
}
