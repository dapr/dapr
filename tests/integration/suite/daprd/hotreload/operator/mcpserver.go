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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserver))
}

// mcpserver verifies that MCPServer resources are detected by the operator hot-reload stream.
// The metadata API does not yet expose MCPServers, so we verify via log matching for now.
// TODO(sicoyle): revise this test to check the metadata API and add full lifecycle
// (add/delete) tests once MCPServers are exposed via metadata.
type mcpserver struct {
	daprd    *daprd.Daprd
	logline  *logline.LogLine
	operator *operator.Operator
}

func (m *mcpserver) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	m.logline = logline.New(t,
		logline.WithStdoutLineContains("MCPServer updated via hot-reload: weather-mcp"),
	)

	m.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true},{"name":"MCPServerResource","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(m.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithLogLineStdout(m.logline),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(m.logline, sentry, m.operator, m.daprd),
	}
}

func (m *mcpserver) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("no MCPServers initially", func(t *testing.T) {
		require.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))
	})

	t.Run("adding an MCPServer via operator update is detected", func(t *testing.T) {
		newServer := mcpserverapi.MCPServer{
			ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp", Namespace: "default"},
			Spec: mcpserverapi.MCPServerSpec{
				Endpoint: mcpserverapi.MCPEndpoint{
					StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
						URL: "http://example.com/mcp",
					},
				},
			},
		}
		m.operator.AddMCPServers(newServer)
		m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
			MCPServer: &newServer,
			EventType: operatorv1.ResourceEventType_CREATED,
		})

		m.logline.EventuallyFoundAll(t)
	})

	// TODO(sicoyle): Once the metadata API exposes MCPServers,
	// replace log assertions with metadata API checks and add full hot-reload lifecycle tests:
	// - Verify MCPServer appears in metadata after add
	// - Delete MCPServer → metadata shows 0
	// - Re-add MCPServer → metadata shows 1
	t.Run("metadata API does not yet expose MCPServers on this branch", func(t *testing.T) {
		assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx),
			"MCPServers are not yet exposed via metadata API; activation logic is in feat-mcp-crd-plus-rest")
	})
}
