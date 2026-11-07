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
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	jwx_jwt "github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/sentry"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(spiffeAuthOldSentry))
}

// spiffeAuthOldSentry verifies that daprd still authenticates to an MCP server
// with a SPIFFE JWT when Sentry is older than daprd and does not implement
// FetchJWTSVID. daprd falls back to the JWT SVID that Sentry issued with the
// X.509 SVID, for the audience requested with --sentry-request-jwt-audiences.
type spiffeAuthOldSentry struct {
	daprd  *daprd.Daprd
	sentry *sentry.Sentry

	issuedJWT      atomic.Value // string
	capturedHeader atomic.Value // string
}

func (s *spiffeAuthOldSentry) Setup(t *testing.T) []framework.Option {
	const audience = "mcp://test-server"
	const appID = "spiffe-mcp-old-sentry"

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

	// The fake Sentry implements SignCertificate only, so FetchJWTSVID returns
	// Unimplemented, as a Sentry from before on-demand minting does.
	s.sentry = sentry.New(t, sentry.WithSignCertificateFn(
		func(_ context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
			bundle := s.sentry.Bundle()
			// Sentry puts workloads that do not name a trust domain in "public".
			td := req.GetTrustDomain()
			if td == "" {
				td = "public"
			}
			id, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString(td), "ns", req.GetNamespace(), req.GetId())
			require.NoError(t, err)

			der, _ := pem.Decode(req.GetCertificateSigningRequest())
			require.NotNil(t, der)
			csr, err := x509.ParseCertificateRequest(der.Bytes)
			require.NoError(t, err)

			cert := x509.Certificate{
				SerialNumber: big.NewInt(1),
				PublicKey:    csr.PublicKey,
				NotBefore:    time.Now(),
				NotAfter:     time.Now().Add(time.Hour),
				URIs:         []*url.URL{id.URL()},
			}
			signed, err := x509.CreateCertificate(rand.Reader, &cert, bundle.X509.IssChain[0], csr.PublicKey, bundle.X509.IssKey)
			require.NoError(t, err)
			signedPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: signed})

			perAudience := make(map[string]*wrapperspb.StringValue)
			for _, aud := range req.GetJwtAudiences() {
				tkn, err := jwx_jwt.NewBuilder().
					Subject(id.String()).
					Audience([]string{aud}).
					IssuedAt(time.Now()).
					Expiration(time.Now().Add(time.Hour)).
					Build()
				require.NoError(t, err)
				jwt, err := jwx_jwt.Sign(tkn, jwx_jwt.WithKey(jwa.RS256, bundle.JWT.SigningKey))
				require.NoError(t, err)
				perAudience[aud] = wrapperspb.String(string(jwt))
				if aud == audience {
					s.issuedJWT.Store(string(jwt))
				}
			}

			return &sentryv1pb.SignCertificateResponse{
				WorkloadCertificate: append(signedPEM, bundle.X509.IssChainPEM...),
				ValidUntil:          timestamppb.New(time.Now().Add(time.Hour)),
				PerAudienceJwts:     perAudience,
			}, nil
		},
	))

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, s.sentry.Bundle().X509.TrustAnchors, 0o600))

	// Workflows, which the MCPServer registers, need actors.
	place := placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(s.sentry.Address(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppID(appID),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(s.sentry.Bundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(s.sentry.Address(t)),
		daprd.WithEnableMTLS(true),
		daprd.WithSentryRequestJwtAudiences(audience),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
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
            audience: %s
`, mcpSrvProc.Port(), audience)),
	)

	return []framework.Option{
		framework.WithProcesses(s.sentry, place, mcpSrvProc, s.daprd),
	}
}

func (s *spiffeAuthOldSentry) Run(t *testing.T, ctx context.Context) {
	// daprd connects to the MCP server while loading it, and exits if it
	// cannot get a JWT.
	s.daprd.WaitUntilRunning(t, ctx)

	issued, ok := s.issuedJWT.Load().(string)
	require.True(t, ok, "expected sentry to issue a JWT for the MCP audience")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		captured, ok := s.capturedHeader.Load().(string)
		if assert.True(c, ok, "expected X-SPIFFE-JWT header to have been captured") {
			assert.Equal(c, "Bearer "+issued, captured)
		}
	}, time.Second*10, time.Millisecond*10)
}
