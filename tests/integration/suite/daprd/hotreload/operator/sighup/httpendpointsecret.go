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

package sighup

import (
	"context"
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
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/exec/kill"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	procoperator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	cryptotest "github.com/dapr/kit/crypto/test"
)

func init() {
	suite.Register(new(httpendpointsecret))
}

type httpendpointsecret struct {
	daprd    *daprd.Daprd
	kubeapi  *kubernetes.Kubernetes
	logOut   *log.Log
	endpoint *httpendpointapi.HTTPEndpoint
}

func (h *httpendpointsecret) Setup(t *testing.T) []framework.Option {
	h.logOut = log.New()

	snt := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))
	certs := cryptotest.GenPKI(t, cryptotest.PKIOptions{LeafDNS: "localhost"})

	h.endpoint = &httpendpointapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "secretendpoint", Namespace: "default"},
		Spec: httpendpointapi.HTTPEndpointSpec{
			BaseURL: "https://localhost:1234",
			ClientTLS: &common.TLS{
				RootCA: &common.TLSDocument{
					SecretKeyRef: &common.SecretKeyRef{Name: "dapr-tls-certificates", Key: "tls.ca"},
				},
			},
		},
	}
	endpoints := store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "HTTPEndpoint",
	})
	endpoints.Add(h.endpoint)

	h.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			snt.Port(),
		),
		kubernetes.WithSecretList(t, &corev1.SecretList{
			Items: []corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{Name: "dapr-tls-certificates", Namespace: "default"},
				Data:       map[string][]byte{"tls.ca": certs.RootCertPEM},
			}},
		}),
		kubernetes.WithClusterDaprHTTPEndpointListFromStore(t, endpoints),
	)

	op := procoperator.New(t,
		procoperator.WithNamespace("default"),
		procoperator.WithKubeconfigPath(h.kubeapi.KubeconfigPath(t)),
		procoperator.WithTrustAnchorsFile(snt.TrustAnchorsFile(t)),
	)

	h.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(op.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
			exec.WithStdout(h.logOut),
			exec.WithStderr(h.logOut),
		),
	)

	return []framework.Option{
		framework.WithProcesses(snt, h.kubeapi, op, h.daprd),
	}
}

func (h *httpendpointsecret) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.daprd.GetMetaHTTPEndpoints(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	// The operator forwards an event only to the watchers registered at that
	// moment, so re-send until the reconciler sees one.
	for deadline := time.Now().Add(time.Second * 10); !h.logOut.Contains("HTTPEndpoint UPDATED event"); time.Sleep(time.Millisecond * 500) {
		require.True(t, time.Now().Before(deadline), "the reconciler never saw the re-sent endpoint")
		h.kubeapi.Informer().Modify(t, h.endpoint)
	}
	assert.False(t, h.logOut.Contains("triggering SIGHUP reload"),
		"an unchanged endpoint with a secret reference must not restart daprd")
	assert.Never(t, func() bool {
		return h.logOut.Contains(kill.HUPLogLine)
	}, time.Second, time.Millisecond*50)
}
