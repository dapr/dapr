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

package jwt

import (
	"context"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jws"
	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dapr/dapr/pkg/healthz"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/cert"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
)

func init() {
	suite.Register(new(ondemand))
}

const (
	sentryID = "spiffe://localhost/ns/default/dapr-sentry"
	issuer   = "https://sentry.example.com"
	appID    = "spiffe://public/ns/default/my-app"
)

// ondemand tests that Sentry mints JWT SVIDs on demand for any audience to
// clients authenticated by their X.509 SVID.
type ondemand struct {
	sentry      *sentry.Sentry
	sentryNoJWT *sentry.Sentry
}

func (o *ondemand) Setup(t *testing.T) []framework.Option {
	o.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuer(issuer),
	)
	o.sentryNoJWT = sentry.New(t, sentry.WithMode("standalone"))

	return []framework.Option{
		framework.WithProcesses(o.sentry, o.sentryNoJWT),
	}
}

func (o *ondemand) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)
	o.sentryNoJWT.WaitUntilRunning(t, ctx)

	keySet := o.sentry.CABundle().JWT.JWKS

	t.Run("daprd security mints a new JWT SVID for an unregistered audience", func(t *testing.T) {
		sctx, cancel := context.WithCancel(ctx)
		secProv, err := security.New(sctx, security.Options{
			SentryAddress:           o.sentry.Address(),
			ControlPlaneTrustDomain: "localhost",
			ControlPlaneNamespace:   "default",
			TrustAnchors:            o.sentry.CABundle().X509.TrustAnchors,
			AppID:                   "my-app",
			MTLSEnabled:             true,
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		errCh := make(chan error)
		go func() { errCh <- secProv.Run(sctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case runErr := <-errCh:
				require.NoError(t, runErr)
			case <-time.After(5 * time.Second):
				assert.Fail(t, "timed out waiting for security provider to stop")
			}
		})

		sec, err := secProv.Handler(sctx)
		require.NoError(t, err)

		minter, ok := spiffecontext.JWTMinterFrom(sec.WithSVIDContext(ctx))
		require.True(t, ok)

		svid1, err := minter.MintJWTSVID(ctx, jwtsvid.Params{Audience: "https://api.anthropic.com"})
		require.NoError(t, err)
		svid2, err := minter.MintJWTSVID(ctx, jwtsvid.Params{Audience: "https://api.anthropic.com"})
		require.NoError(t, err)
		raw1, raw2 := svid1.Marshal(), svid2.Marshal()

		tkn1, err := jwxjwt.Parse([]byte(raw1), jwxjwt.WithKeySet(keySet))
		require.NoError(t, err)
		tkn2, err := jwxjwt.Parse([]byte(raw2), jwxjwt.WithKeySet(keySet))
		require.NoError(t, err)

		assert.Equal(t, issuer, tkn1.Issuer())
		assert.Equal(t, appID, tkn1.Subject())
		assert.Equal(t, []string{"https://api.anthropic.com"}, tkn1.Audience())
		assert.Equal(t, 5*time.Minute, tkn1.Expiration().Sub(tkn1.IssuedAt()))
		assert.NotEmpty(t, tkn1.JwtID())
		assert.NotEqual(t, tkn1.JwtID(), tkn2.JwtID(), "each fetch must mint a new token")

		msg, err := jws.Parse([]byte(raw1))
		require.NoError(t, err)
		require.Len(t, msg.Signatures(), 1)
		kid := msg.Signatures()[0].ProtectedHeaders().KeyID()
		require.NotEmpty(t, kid)
		_, ok = keySet.LookupKeyID(kid)
		assert.True(t, ok, "kid must be in Sentry's JWKS")
	})

	t.Run("audiences and ttl are taken from the request, ttl capped at the JWT TTL", func(t *testing.T) {
		client := sentryv1pb.NewCAClient(o.sentry.DialGRPCMTLS(t, sentryID, o.sentry.SignSVID(t, ctx, sentryID, "default", "my-app")))

		resp, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{
			Audiences: []string{"a", "b"},
			Ttl:       durationpb.New(48 * time.Hour),
		})
		require.NoError(t, err)
		tkn, err := jwxjwt.Parse([]byte(resp.GetJwt()), jwxjwt.WithKeySet(keySet))
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, tkn.Audience())
		assert.Equal(t, 24*time.Hour, tkn.Expiration().Sub(tkn.IssuedAt()))

		_, err = client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{})
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
		_, err = client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{Audiences: []string{"a"}, Ttl: durationpb.New(-time.Second)})
		assert.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
	})

	t.Run("clients without a certificate are rejected", func(t *testing.T) {
		client := sentryv1pb.NewCAClient(o.sentry.DialGRPC(t, ctx, sentryID))
		_, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{Audiences: []string{"a"}})
		assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	})

	t.Run("certificates not signed by the Sentry CA are rejected", func(t *testing.T) {
		client := sentryv1pb.NewCAClient(o.sentry.DialGRPCMTLS(t, sentryID, cert.SelfSignedSVID(t, spiffeid.RequireFromString(appID))))
		_, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{Audiences: []string{"a"}})
		assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
	})

	t.Run("SPIFFE IDs with extra path segments are rejected", func(t *testing.T) {
		ca := o.sentry.CABundle().X509
		client := sentryv1pb.NewCAClient(o.sentry.DialGRPCMTLS(t, sentryID, cert.IssuedSVID(t, spiffeid.RequireFromString(appID+"/extra"), ca.IssChain, ca.IssKey)))
		_, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{Audiences: []string{"a"}})
		assert.Equal(t, codes.PermissionDenied, status.Code(err), "%v", err)
	})

	t.Run("sentry without JWT issuing reports a failed precondition", func(t *testing.T) {
		client := sentryv1pb.NewCAClient(o.sentryNoJWT.DialGRPCMTLS(t, sentryID, o.sentryNoJWT.SignSVID(t, ctx, sentryID, "default", "my-app")))
		_, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{Audiences: []string{"a"}})
		assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	})
}
