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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	mcpapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

// scopes verifies that MCPServer resources respect app scoping via operator mode.
type scopes struct {
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
	logline1 *logline.LogLine
	logline2 *logline.LogLine
}

func (s *scopes) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	// daprd1 ("myapp") should load global-mcp and scoped-mcp.
	s.logline1 = logline.New(t,
		logline.WithStdoutLineContains(
			"MCPServer loaded: global-mcp",
			"MCPServer loaded: scoped-mcp",
		),
	)
	// daprd2 ("otherapp") should load only global-mcp.
	s.logline2 = logline.New(t,
		logline.WithStdoutLineContains("MCPServer loaded: global-mcp"),
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
		}),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Name: "mcpconfig", Namespace: "default"},
			}},
		}),
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "scoped-mcp", Namespace: "default"},
					Spec: mcpapi.MCPServerSpec{
						Endpoint: mcpapi.MCPEndpoint{
							StreamableHTTP: &mcpapi.MCPStreamableHTTP{URL: "http://scoped.example.com/mcp"},
						},
					},
					Scoped: commonapi.Scoped{Scopes: []string{"myapp"}},
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
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("myapp"),
		daprd.WithLogLineStdout(s.logline1),
		daprd.WithConfigs("mcpconfig"),
	)

	s.daprd2 = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentry(t, sentry),
		daprd.WithControlPlaneAddress(opr.Address()),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("otherapp"),
		daprd.WithLogLineStdout(s.logline2),
		daprd.WithConfigs("mcpconfig"),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline1, s.logline2, sentry, kubeapi, opr, s.daprd1, s.daprd2),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	t.Run("myapp loads global-mcp and scoped-mcp", func(t *testing.T) {
		s.logline1.EventuallyFoundAll(t)
	})

	t.Run("otherapp loads global-mcp only", func(t *testing.T) {
		s.logline2.EventuallyFoundAll(t)
	})

	t.Run("metadata API exposes scoped MCPServers per app", func(t *testing.T) {
		// myapp is in scoped-mcp's scopes, so it loads both global-mcp and
		// scoped-mcp.
		myappNames := mcpServerNames(s.daprd1.GetMetaMCPServers(t, ctx))
		assert.ElementsMatch(t, []string{"global-mcp", "scoped-mcp"}, myappNames,
			"myapp should expose global-mcp + scoped-mcp")

		// otherapp is not in scoped-mcp's scopes, so it loads only global-mcp.
		otherappNames := mcpServerNames(s.daprd2.GetMetaMCPServers(t, ctx))
		assert.ElementsMatch(t, []string{"global-mcp"}, otherappNames,
			"otherapp should expose global-mcp only")
	})
}

// mcpServerNames extracts the Name field from a slice of MetadataMCPServer.
func mcpServerNames(servers []*runtimev1pb.MetadataMCPServer) []string {
	names := make([]string, 0, len(servers))
	for _, m := range servers {
		names = append(names, m.GetName())
	}
	return names
}
