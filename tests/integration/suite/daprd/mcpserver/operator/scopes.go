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
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(scopes))
}

// scopes verifies that MCPServer resources respect app scoping:
// - An MCPServer with no scopes is loaded by all apps.
// - An MCPServer scoped to "myapp" is loaded by "myapp" but not "otherapp".
// - An MCPServer in a different namespace is not loaded.
type scopes struct {
	daprd1 *daprd.Daprd // app "myapp" in "default" namespace
	daprd2 *daprd.Daprd // app "otherapp" in "default" namespace

	logline1 *logline.LogLine
	logline2 *logline.LogLine
}

func (s *scopes) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	// daprd1 ("myapp") should load both global-mcp and scoped-mcp.
	s.logline1 = logline.New(t,
		logline.WithStdoutLineContains(
			"MCPServer loaded: global-mcp",
			"MCPServer loaded: scoped-mcp",
		),
	)
	// daprd2 ("otherapp") should load only global-mcp (scoped-mcp is restricted to myapp).
	s.logline2 = logline.New(t,
		logline.WithStdoutLineContains("MCPServer loaded: global-mcp"),
	)

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprMCPServerList(t, &mcpapi.MCPServerList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "MCPServerList"},
			Items: []mcpapi.MCPServer{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "global-mcp", Namespace: "default"},
					Spec: mcpapi.MCPServerSpec{
						Endpoint: mcpapi.MCPEndpoint{
							StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://example.com/mcp"},
						},
					},
					// No scopes — available to all apps.
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "scoped-mcp", Namespace: "default"},
					Spec: mcpapi.MCPServerSpec{
						Endpoint: mcpapi.MCPEndpoint{
							StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://scoped.example.com/mcp"},
						},
					},
					Scoped: scopedTo("myapp"),
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "other-ns-mcp", Namespace: "other"},
					Spec: mcpapi.MCPServerSpec{
						Endpoint: mcpapi.MCPEndpoint{
							StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://other-ns.example.com/mcp"},
						},
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

	s.daprd1 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentry(t, sentry),
		daprd.WithControlPlaneAddress(opr.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("myapp"),
		daprd.WithLogLineStdout(s.logline1),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpconfig
spec:
  features:
  - name: MCPServerResource
    enabled: true
`),
	)

	s.daprd2 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentry(t, sentry),
		daprd.WithControlPlaneAddress(opr.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("otherapp"),
		daprd.WithLogLineStdout(s.logline2),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpconfig
spec:
  features:
  - name: MCPServerResource
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, kubeapi, opr, s.daprd1, s.daprd2),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	t.Run("myapp loads global-mcp and scoped-mcp", func(t *testing.T) {
		s.logline1.EventuallyFoundAll(t)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd1.GetMetaMCPServers(c, ctx)
			names := mcpServerNames(servers)
			assert.Contains(c, names, "global-mcp")
			assert.Contains(c, names, "scoped-mcp")
			assert.NotContains(c, names, "other-ns-mcp")
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("otherapp loads global-mcp only", func(t *testing.T) {
		s.logline2.EventuallyFoundAll(t)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd2.GetMetaMCPServers(c, ctx)
			names := mcpServerNames(servers)
			assert.Contains(c, names, "global-mcp")
			assert.NotContains(c, names, "scoped-mcp")
		}, 10*time.Second, 100*time.Millisecond)
	})
}

func mcpServerNames(servers []*rtv1.MetadataMCPServer) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.GetName()
	}
	return names
}

func scopedTo(appIDs ...string) commonapi.Scoped {
	return commonapi.Scoped{Scopes: appIDs}
}
