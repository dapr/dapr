/*
Copyright 2023 The Dapr Authors
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

package standalone

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/tests/integration/suite/sentry/utils"
	secpem "github.com/dapr/kit/crypto/pem"
)

func init() {
	suite.Register(new(standalone))
}

// standalone tests Sentry in standalone mode in kubernetes
type standalone struct {
	sentry *sentry.Sentry
}

func (k *standalone) Setup(t *testing.T) []framework.Option {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	kubeAPI := utils.KubeAPI(t, utils.KubeAPIOptions{
		Bundle:         bundle,
		Namespace:      "mynamespace",
		ServiceAccount: "myserviceaccount",
		AppID:          "myappid",
	})

	k.sentry = sentry.New(t,
		sentry.WithNamespace("sentrynamespace"),
		sentry.WithMode(string(modes.StandaloneMode)),
		sentry.WithCABundle(bundle),
		sentry.WithTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(k.sentry, kubeAPI),
	}
}

func (k *standalone) Run(t *testing.T, ctx context.Context) {
	k.sentry.WaitUntilRunning(t, ctx)

	conn := k.sentry.DialGRPC(t, ctx, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry")
	client := sentrypbv1.NewCAClient(conn)

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)

	resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
		Id:                        "myappid",
		Namespace:                 "mynamespace",
		CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
		TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.GetWorkloadCertificate())

	// Validate certificate chain
	certs, err := secpem.DecodePEMCertificates(resp.GetWorkloadCertificate())
	require.NoError(t, err)
	require.Len(t, certs, 2)
	require.NoError(t, certs[0].CheckSignatureFrom(certs[1]))
	require.Len(t, k.sentry.CABundle().IssChain, 1)
	assert.Equal(t, k.sentry.CABundle().IssChain[0].Raw, certs[1].Raw)
	trustBundle, err := secpem.DecodePEMCertificates(k.sentry.CABundle().TrustAnchors)
	require.NoError(t, err)
	require.Len(t, trustBundle, 1)
	require.NoError(t, certs[1].CheckSignatureFrom(trustBundle[0]))

	// Test JWT validation and TTL behavior
	t.Run("validate JWT token with standard TTL", func(t *testing.T) {
		require.NotEmpty(t, resp.GetJwt(), "JWT token should be included in the response")

		// Parse and verify the JWT token
		token, err := jwt.Parse([]byte(resp.GetJwt()), jwt.WithVerify(false)) // Skip signature verification
		require.NoError(t, err, "JWT token should be parsable")

		// Validate the subject claim matches the expected SPIFFE ID
		expectedSubject := "spiffe://integration.test.dapr.io/ns/mynamespace/myappid"
		sub, found := token.Get("sub")
		require.True(t, found, "subject claim should exist in JWT")
		assert.Equal(t, expectedSubject, sub, "JWT subject should match SPIFFE ID")

		// Validate expiration and issuance time
		exp, found := token.Get("exp")
		require.True(t, found, "exp claim should exist in JWT")
		require.NotNil(t, exp, "expiration time should not be nil")

		iat, found := token.Get("iat")
		require.True(t, found, "iat claim should exist in JWT")
		require.NotNil(t, iat, "issued at time should not be nil")

		// Verify expiration is set properly (default is 1 hour)
		expTime := exp.(time.Time)
		iatTime := iat.(time.Time)
		ttlDuration := expTime.Sub(iatTime)
		assert.GreaterOrEqual(t, ttlDuration.Hours(), float64(1), "Default TTL should be at least 1 hour")
	})

	// Test with explicit TTL
	t.Run("validate JWT token with explicit TTL", func(t *testing.T) {
		requestTTL := 30 * time.Minute

		resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        "myappid",
			Namespace:                 "mynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
			Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetJwt(), "JWT token should be included in the response")

		// Parse and verify the JWT token
		token, err := jwt.Parse([]byte(resp.GetJwt()), jwt.WithVerify(false))
		require.NoError(t, err, "JWT token should be parsable")

		// Verify expiration matches the requested TTL
		exp, found := token.Get("exp")
		require.True(t, found, "exp claim should exist in JWT")

		iat, found := token.Get("iat")
		require.True(t, found, "iat claim should exist in JWT")

		expTime := exp.(time.Time)
		iatTime := iat.(time.Time)
		ttlDuration := expTime.Sub(iatTime)

		// Allow small margin of error (1 second) for processing time
		assert.InDelta(t, requestTTL.Seconds(), ttlDuration.Seconds(), 1,
			"JWT expiration should match the requested TTL")
	})

	// Test with zero TTL (should use default TTL)
	t.Run("validate JWT token with zero TTL", func(t *testing.T) {
		resp, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        "myappid",
			Namespace:                 "mynamespace",
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer}),
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
			Token:                     `{"kubernetes.io":{"pod":{"name":"mypod"}}}`,
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetJwt(), "JWT token should be included in the response")

		// Parse and verify the JWT token
		token, err := jwt.Parse([]byte(resp.GetJwt()), jwt.WithVerify(false))
		require.NoError(t, err, "JWT token should be parsable")

		// Verify default expiration is set
		exp, found := token.Get("exp")
		require.True(t, found, "exp claim should exist in JWT")

		iat, found := token.Get("iat")
		require.True(t, found, "iat claim should exist in JWT")

		expTime := exp.(time.Time)
		iatTime := iat.(time.Time)
		ttlDuration := expTime.Sub(iatTime)

		// Default TTL should be at least 1 hour
		assert.GreaterOrEqual(t, ttlDuration.Hours(), float64(1), "Default TTL should be at least 1 hour")
	})
}
