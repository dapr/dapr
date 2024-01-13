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

package jwks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
)

// Base class for the JWKS tests
type shared struct {
	proc *procsentry.Sentry
}

func (s shared) Run(t *testing.T, parentCtx context.Context) {
	const (
		defaultAppID     = "myapp"
		defaultNamespace = "default"
	)
	defaultAppSPIFFEID := fmt.Sprintf("spiffe://public/ns/%s/%s", defaultNamespace, defaultAppID)
	defaultAppDNSName := fmt.Sprintf("%s.%s.svc.cluster.local", defaultAppID, defaultNamespace)

	s.proc.WaitUntilRunning(t, parentCtx)

	// Generate a private privKey that we'll need for tests
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "failed to generate private key")

	// Get a CSR for the app "myapp"
	defaultAppCSR, err := generateCSR(defaultAppID, privKey)
	require.NoError(t, err)

	// Connect to Sentry
	conn := s.proc.DialGRPC(t, parentCtx, "spiffe://localhost/ns/default/dapr-sentry")
	client := sentrypbv1.NewCAClient(conn)

	t.Run("fails when passing an invalid validator", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:             defaultAppID,
			Namespace:      defaultNamespace,
			TokenValidator: sentrypbv1.SignCertificateRequest_TokenValidator(-1), // -1 is an invalid enum value
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "not enabled")
	})

	t.Run("fails when passing the insecure validator", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:             defaultAppID,
			Namespace:      defaultNamespace,
			TokenValidator: sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "not enabled")
	})

	t.Run("fails when no validator is passed", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:        defaultAppID,
			Namespace: defaultNamespace,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "a validator name must be specified in this environment")
	})

	t.Run("issue a certificate with JWKS validator", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()

		var token []byte
		token, err = signJWT(generateJWT(defaultAppSPIFFEID))
		require.NoError(t, err)

		var res *sentrypbv1.SignCertificateResponse
		res, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        defaultAppID,
			Namespace:                 defaultNamespace,
			CertificateSigningRequest: defaultAppCSR,
			TokenValidator:            sentrypbv1.SignCertificateRequest_JWKS,
			Token:                     string(token),
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.GetWorkloadCertificate())

		validateCertificateResponse(t, res, s.proc.CABundle(), defaultAppSPIFFEID, defaultAppDNSName)
	})

	testWithTokenError := func(fn func(builder *jwt.Builder), assertErr func(t *testing.T, grpcStatus *status.Status)) func(t *testing.T) {
		return func(t *testing.T) {
			ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
			defer cancel()

			var token []byte
			builder := generateJWT(defaultAppSPIFFEID)
			fn(builder)
			token, err = signJWT(builder)
			require.NoError(t, err)

			_, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
				Id:                        defaultAppID,
				Namespace:                 defaultNamespace,
				CertificateSigningRequest: defaultAppCSR,
				TokenValidator:            sentrypbv1.SignCertificateRequest_JWKS,
				Token:                     string(token),
			})
			require.Error(t, err)

			grpcStatus, ok := status.FromError(err)
			require.True(t, ok)
			assertErr(t, grpcStatus)
		}
	}

	t.Run("fails when token has invalid audience", testWithTokenError(
		func(builder *jwt.Builder) {
			builder.Audience([]string{"invalid"})
		},
		func(t *testing.T, grpcStatus *status.Status) {
			require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
			require.Contains(t, grpcStatus.Message(), "token validation failed")
			require.Contains(t, grpcStatus.Message(), `"aud"`)
		},
	))

	t.Run("fails when token has invalid subject", testWithTokenError(
		func(builder *jwt.Builder) {
			builder.Subject("invalid")
		},
		func(t *testing.T, grpcStatus *status.Status) {
			require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
			require.Contains(t, grpcStatus.Message(), "token validation failed")
			require.Contains(t, grpcStatus.Message(), `"sub"`)
		},
	))

	t.Run("fails when token is expired", testWithTokenError(
		func(builder *jwt.Builder) {
			builder.Expiration(time.Now().Add(-1 * time.Hour))
		},
		func(t *testing.T, grpcStatus *status.Status) {
			require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
			require.Contains(t, grpcStatus.Message(), "token validation failed")
			require.Contains(t, grpcStatus.Message(), `"exp"`)
		},
	))

	t.Run("fails when token is not yet valid", testWithTokenError(
		func(builder *jwt.Builder) {
			builder.NotBefore(time.Now().Add(1 * time.Hour))
		},
		func(t *testing.T, grpcStatus *status.Status) {
			require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
			require.Contains(t, grpcStatus.Message(), "token validation failed")
			require.Contains(t, grpcStatus.Message(), `"nbf"`)
		},
	))

	t.Run("fails with token signed by wrong key", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()

		//nolint:gosec
		const token = `eyJhbGciOiJSUzI1NiIsImtpZCI6Im15a2V5IiwidHlwIjoiSldUIn0.eyJhdWQiOlsic3BpZmZlOi8vbG9jYWxob3N0L25zL2RlZmF1bHQvZGFwci1zZW50cnkiXSwiZXhwIjoxOTg5MzEwMjY1LCJpYXQiOjE2ODkzMDY2NjUsInN1YiI6InNwaWZmZTovL3B1YmxpYy9ucy9kZWZhdWx0L215YXBwIn0.JZ5fIss3IWjdvOoUTGyTgIsXqfwj7GClno1kgTDd5KKKY4Qwe16CMM4fk1sDIkm09FCD8sTGJzx3MEb9Ls3blsuu3VSNjTxJZrGs3M9ZAFlaS7OGod-8DYMnF-dfQzY-li7VvXIZT3h92DKdOTqVpNgETGZi_7Qjdkkpz8elkK957VVZXz1q8wAW4tOD8Qe6nGnys2q-ksfC0zR39YSVAxM2hGUklxBOMNhqocBGVmYqwJC31NwKwjLy-Ryorcjv4-DCLgKpvQd-MFJYrJU7ztCdBiIBR51btzRHVbtlx9CeESwcC8pNBzDXABOWhQ4L4Prt4qA3hBLCxPvsE-vgjA`

		_, err = client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        defaultAppID,
			Namespace:                 defaultNamespace,
			CertificateSigningRequest: defaultAppCSR,
			TokenValidator:            sentrypbv1.SignCertificateRequest_JWKS,
			Token:                     token,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "token validation failed")
		require.Contains(t, grpcStatus.Message(), `could not verify message`)
	})
}
