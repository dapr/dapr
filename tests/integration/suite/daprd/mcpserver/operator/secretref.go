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

package operator

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	mcpapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secretref))
}

// secretref verifies that the operator resolves secretKeyRef entries in
// MCPServer spec.endpoint.streamableHTTP.headers using the Kubernetes secret store,
// and daprd receives the resolved values.
type secretref struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (s *secretref) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	s.logline = logline.New(t,
		logline.WithStdoutLineContains("MCPServer loaded: secreted-mcp"),
	)

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithDaprConfigurationGet(t, &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Name: "mcpconfig", Namespace: "default"},
			Spec: configapi.ConfigurationSpec{
				Features: []configapi.FeatureSpec{
					{Name: "MCPServerResource", Enabled: new(true)},
				},
			},
		}),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Name: "mcpconfig", Namespace: "default"},
				Spec: configapi.ConfigurationSpec{
					Features: []configapi.FeatureSpec{
						{Name: "MCPServerResource", Enabled: new(true)},
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprMCPServerList(t, &mcpapi.MCPServerList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "MCPServerList"},
			Items: []mcpapi.MCPServer{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "secreted-mcp", Namespace: "default"},
					Spec: mcpapi.MCPServerSpec{
						Endpoint: mcpapi.MCPEndpoint{
							StreamableHTTP: &mcpapi.MCPStreamableHTTP{
								URL: "http://example.com/mcp",
								Headers: []commonapi.NameValuePair{
									{
										Name: "Authorization",
										SecretKeyRef: commonapi.SecretKeyRef{
											Name: "mcp-token-secret",
											Key:  "token",
										},
									},
								},
							},
						},
					},
				},
			},
		}),
		// Provide the Kubernetes secret that the operator will resolve.
		kubernetes.WithSecretList(t, &corev1.SecretList{
			Items: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mcp-token-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"token": []byte("my-secret-bearer-token"),
					},
				},
			},
		}),
	)

	opr := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentry(t, sentry),
		daprd.WithControlPlaneAddress(opr.Address()),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("test-app"),
		daprd.WithLogLineStdout(s.logline),
		daprd.WithConfigs("mcpconfig"),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, sentry, kubeapi, opr, s.daprd),
	}
}

func (s *secretref) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("MCPServer with secretKeyRef header is loaded via operator", func(t *testing.T) {
		s.logline.EventuallyFoundAll(t)
	})

	// TODO(sicoyle): Once the metadata API exposes MCPServers, add metadata API
	// checks to verify secreted-mcp appears with resolved headers.
	t.Run("metadata API does not yet expose MCPServers on this branch", func(t *testing.T) {
		assert.Empty(t, s.daprd.GetMetaMCPServers(t, ctx))
	})
}
