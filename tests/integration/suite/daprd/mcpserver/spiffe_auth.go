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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lestrrat-go/jwx/v2/jwk"
	jwx_jwt "github.com/lestrrat-go/jwx/v2/jwt"
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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(spiffeAuth))
}

type spiffeAuth struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	sent       *sentry.Sentry
	httpClient *http.Client

	capturedHeader atomic.Value // stores string
}

func (s *spiffeAuth) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-spiffe-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes input",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, struct{}{}, nil
	})

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if h := r.Header.Get("X-SPIFFE-JWT"); h != "" {
			s.capturedHeader.Store(h)
		}
		return mcpSrv
	}, nil)
	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(mcpHandler))

	appProc := app.New(t)

	s.sent = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithOIDCEnabled(true),
		sentry.WithJWTIssuerFromOIDC(),
	)

	bundle := s.sent.CABundle()
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	s.sched = scheduler.New(t,
		scheduler.WithSentry(s.sent),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	s.place = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(s.sent.Address()),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppID("spiffe-mcp-app"),
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(s.sent.Address()),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
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
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: spiffe-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
      auth:
        spiffe:
          jwt:
            header: X-SPIFFE-JWT
            headerValuePrefix: "Bearer "
            audience: mcp://test-server
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.sent, s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *spiffeAuth) Run(t *testing.T, ctx context.Context) {
	s.sent.WaitUntilRunning(t, ctx)
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("SPIFFE JWT is injected with correct SPIFFE ID and signed by Sentry", func(t *testing.T) {
		input := map[string]any{
			"tool_name": "echo",
			"arguments": map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("spiffe-server", "echo"), input)

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.GetIsError(), "expected tool call to succeed")

		// Extract the captured JWT from the MCP server.
		capturedJWT, ok := s.capturedHeader.Load().(string)
		require.True(t, ok, "expected X-SPIFFE-JWT header to have been captured")
		require.True(t, strings.HasPrefix(capturedJWT, "Bearer "), "expected Bearer prefix; got: %s", capturedJWT)
		tokenRaw := strings.TrimPrefix(capturedJWT, "Bearer ")

		// Fetch JWKS from Sentry's OIDC endpoint to verify the signature.
		insecureClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
		oidcURL := fmt.Sprintf("https://localhost:%d/.well-known/openid-configuration", s.sent.OIDCPort(t))
		resp, err := insecureClient.Get(oidcURL)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var discovery struct {
			Issuer  string `json:"issuer"`
			JWKSURI string `json:"jwks_uri"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&discovery))
		require.NotEmpty(t, discovery.JWKSURI)

		jwksResp, err := insecureClient.Get(discovery.JWKSURI)
		require.NoError(t, err)
		defer jwksResp.Body.Close()
		require.Equal(t, http.StatusOK, jwksResp.StatusCode)

		keySet, err := jwk.ParseReader(jwksResp.Body)
		require.NoError(t, err)
		require.Positive(t, keySet.Len(), "JWKS should contain at least one key")

		// Parse and validate the JWT signature using Sentry's JWKS.
		tkn, err := jwx_jwt.Parse([]byte(tokenRaw), jwx_jwt.WithKeySet(keySet))
		require.NoError(t, err, "JWT should be valid and signed by Sentry")

		// Verify the SPIFFE ID is the correct app identity.
		sub, ok := tkn.Get("sub")
		require.True(t, ok, "JWT should have a sub claim")
		subStr, ok := sub.(string)
		require.True(t, ok)
		assert.Contains(t, subStr, "spiffe-mcp-app", "sub claim should contain the app ID")

		// Verify the audience matches what was configured.
		aud, ok := tkn.Get("aud")
		require.True(t, ok, "JWT should have an aud claim")
		audList, ok := aud.([]string)
		require.True(t, ok)
		assert.Contains(t, audList, "mcp://test-server", "aud should contain the configured audience")

		// Verify issuer matches Sentry's OIDC issuer.
		issuer, _ := tkn.Get("iss")
		assert.Equal(t, discovery.Issuer, issuer, "issuer should match Sentry OIDC")
	})
}
