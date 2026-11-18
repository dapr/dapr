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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	jwxjwt "github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	cryptopem "github.com/dapr/kit/crypto/pem"
)

func init() {
	suite.Register(new(certttl))
}

// certttl tests that a JWT SVID never outlives the X.509 SVID it comes with,
// when the workload certificate TTL is shorter than the JWT TTL.
type certttl struct {
	sentry *sentry.Sentry
}

func (c *certttl) Setup(t *testing.T) []framework.Option {
	c.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuer(issuer),
		sentry.WithJWTTTL(24*time.Hour),
		sentry.WithConfiguration(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sentryconfig
spec:
  mtls:
    enabled: true
    workloadCertTTL: "1h"
`),
	)

	return []framework.Option{
		framework.WithProcesses(c.sentry),
	}
}

func (c *certttl) Run(t *testing.T, ctx context.Context) {
	c.sentry.WaitUntilRunning(t, ctx)

	keySet := c.sentry.CABundle().JWT.JWKS

	t.Run("SignCertificate caps the JWT at the signed certificate", func(t *testing.T) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		csr, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), key)
		require.NoError(t, err)

		resp, err := sentryv1pb.NewCAClient(c.sentry.DialGRPC(t, ctx, sentryID)).SignCertificate(ctx, &sentryv1pb.SignCertificateRequest{
			Id:                        "my-app",
			Namespace:                 "default",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}),
			TokenValidator:            sentryv1pb.SignCertificateRequest_INSECURE,
			JwtAudiences:              []string{"a"},
		})
		require.NoError(t, err)

		certs, err := cryptopem.DecodePEMCertificates(resp.GetWorkloadCertificate())
		require.NoError(t, err)
		notAfter := certs[0].NotAfter
		assert.WithinDuration(t, time.Now().Add(time.Hour), notAfter, time.Minute)

		jwts := make([]string, 0, 1+len(resp.GetPerAudienceJwts()))
		jwts = append(jwts, resp.GetJwt().GetValue())
		for _, v := range resp.GetPerAudienceJwts() {
			jwts = append(jwts, v.GetValue())
		}
		for _, raw := range jwts {
			tkn, err := jwxjwt.Parse([]byte(raw), jwxjwt.WithKeySet(keySet))
			require.NoError(t, err)
			assert.False(t, tkn.Expiration().After(notAfter), "JWT expires at %s, after the certificate at %s", tkn.Expiration(), notAfter)
			assert.LessOrEqual(t, tkn.Expiration().Sub(tkn.IssuedAt()), time.Hour)
		}
	})

	t.Run("FetchJWTSVID caps the JWT at the client certificate", func(t *testing.T) {
		svid := c.sentry.SignSVID(t, ctx, sentryID, "default", "my-app")
		notAfter := svid.Certificates[0].NotAfter
		assert.WithinDuration(t, time.Now().Add(time.Hour), notAfter, time.Minute)

		client := sentryv1pb.NewCAClient(c.sentry.DialGRPCMTLS(t, sentryID, svid))
		resp, err := client.FetchJWTSVID(ctx, &sentryv1pb.FetchJWTSVIDRequest{
			Audiences: []string{"a"},
			Ttl:       durationpb.New(24 * time.Hour),
		})
		require.NoError(t, err)

		tkn, err := jwxjwt.Parse([]byte(resp.GetJwt()), jwxjwt.WithKeySet(keySet))
		require.NoError(t, err)
		assert.False(t, tkn.Expiration().After(notAfter), "JWT expires at %s, after the certificate at %s", tkn.Expiration(), notAfter)
		assert.LessOrEqual(t, tkn.Expiration().Sub(tkn.IssuedAt()), time.Hour)
	})
}
