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

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"

	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(oauth2Auth))
}

// oauth2Auth verifies that the OAuth2 client credentials flow is used when auth.oauth2 is configured.
// A fake token server issues an access token,
// and the MCP server handler captures the Authorization header to confirm the bearer token was injected.
type oauth2Auth struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client

	capturedAuthHeader atomic.Value // stores string
}

func (s *oauth2Auth) Setup(t *testing.T) []framework.Option {
	const fakeAccessToken = "test-oauth2-bearer-token-xyz" //nolint:gosec // test-only fake token

	// Fake OAuth2 token endpoint.
	tokenHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, fakeAccessToken)
	})
	tokenServer := prochttp.New(t, prochttp.WithHandler(tokenHandler))

	// MCP server that captures the Authorization header.
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-oauth2-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes input",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, struct{}{}, nil
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if auth := r.Header.Get("Authorization"); auth != "" {
			s.capturedAuthHeader.Store(auth)
		}
		return mcpSrv
	}, nil)
	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(mcpHandler))

	appProc := app.New(t)
	s.sched = scheduler.New(t)
	s.place = placement.New(t)

	// In-memory secret store with the client_secret value.
	// The MCPServer's oauth2.secretKeyRef points to this.
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
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
		daprd.WithResourceFiles(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: oauth2-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
  auth:
    secretStore: inmemory
    oauth2:
      issuer: http://localhost:%d/token
      clientID: test-client-id
      secretKeyRef:
        name: mcp-oauth-secret
        key: client_secret
`, mcpSrvProc.Port(), tokenServer.Port()),
			// In-memory secret store component.
			`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmemory
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: ""
  - name: multiValued
    value: "false"
`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, tokenServer, mcpSrvProc, s.daprd),
	}
}

func (s *oauth2Auth) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("OAuth2 bearer token is injected into MCP requests", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "oauth2-server",
			"toolName":      "echo",
			"arguments":     map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("oauth2-server", "echo"), input)

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.GetIsError(), "expected tool call to succeed; isError=true, content: %v", result.GetContent())

		// Verify the MCP server received a Bearer token from the OAuth2 flow.
		capturedAuth, ok := s.capturedAuthHeader.Load().(string)
		require.True(t, ok, "expected Authorization header to have been captured")
		assert.True(t, strings.HasPrefix(capturedAuth, "Bearer "),
			"expected Bearer token; got: %s", capturedAuth)
	})
}
