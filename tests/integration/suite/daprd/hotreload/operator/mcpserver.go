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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserver))
}

// mcpserver verifies MCPServer hot-reload lifecycle via the operator stream
// and the metadata API: add, verify in metadata, delete, verify removed.
type mcpserver struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
}

func (m *mcpserver) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

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
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.operator, m.daprd),
	}
}

func (m *mcpserver) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("no MCPServers initially", func(t *testing.T) {
		require.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))
	})

	weatherMCP := mcpserverapi.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp", Namespace: "default"},
		Spec: mcpserverapi.MCPServerSpec{
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: "http://example.com/mcp",
				},
			},
		},
	}

	t.Run("add MCPServer appears in metadata", func(t *testing.T) {
		m.operator.AddMCPServers(weatherMCP)
		m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
			MCPServer: &weatherMCP,
			EventType: operatorv1.ResourceEventType_CREATED,
		})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 1)
			if len(servers) > 0 {
				assert.Equal(c, "weather-mcp", servers[0].GetName())
			}
		}, 10*time.Second, 200*time.Millisecond)
	})

	t.Run("delete MCPServer removes from metadata", func(t *testing.T) {
		m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
			MCPServer: &weatherMCP,
			EventType: operatorv1.ResourceEventType_DELETED,
		})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Empty(c, servers)
		}, 10*time.Second, 200*time.Millisecond)
	})
}
