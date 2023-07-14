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

package validator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(insecure))
}

// insecure tests Sentry with the insecure validator.
type insecure struct {
	proc *procsentry.Sentry
}

func (m *insecure) Setup(t *testing.T) []framework.Option {
	m.proc = procsentry.New(t)

	return []framework.Option{
		framework.WithProcesses(m.proc),
	}
}

func (m *insecure) Run(t *testing.T, parentCtx context.Context) {
	const (
		defaultAppID     = "myapp"
		defaultNamespace = "default"
	)
	defaultAppSPIFFEID := fmt.Sprintf("spiffe://public/ns/%s/%s", defaultNamespace, defaultAppID)
	defaultAppDNSName := fmt.Sprintf("%s.%s.svc.cluster.local", defaultAppID, defaultNamespace)

	m.proc.WaitUntilRunning(t, parentCtx)

	// Generate a private privKey that we'll need for tests
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "failed to generate private key")

	// Get a CSR for the app "myapp"
	defaultAppCSR, err := generateCSR(defaultAppID, privKey)
	require.NoError(t, err)

	// Connect to Sentry
	conn, err := m.proc.ConnectGrpc(parentCtx)
	require.NoError(t, err)
	client := sentrypbv1.NewCAClient(conn)

	t.Run("fails when passing an invalid validator", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
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

	t.Run("issue a certificate with insecure validator", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		res, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        defaultAppID,
			Namespace:                 defaultNamespace,
			CertificateSigningRequest: defaultAppCSR,
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.WorkloadCertificate)

		validateCertificateResponse(t, res, m.proc.CABundle(), defaultAppSPIFFEID, defaultAppDNSName)
	})

	t.Run("insecure validator is the default", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		res, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        defaultAppID,
			Namespace:                 defaultNamespace,
			CertificateSigningRequest: defaultAppCSR,
			// TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.NoError(t, err)
		require.NotEmpty(t, res.WorkloadCertificate)

		validateCertificateResponse(t, res, m.proc.CABundle(), defaultAppSPIFFEID, defaultAppDNSName)
	})

	t.Run("fails with missing CSR", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:        defaultAppID,
			Namespace: defaultNamespace,
			// CertificateSigningRequest: defaultAppCSR,
			TokenValidator: sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "CSR is required")
	})

	t.Run("fails with missing namespace", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id: defaultAppID,
			// Namespace:                 defaultNamespace,
			CertificateSigningRequest: defaultAppCSR,
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.PermissionDenied, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "namespace is required")
	})

	t.Run("fails with invalid CSR", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
		defer cancel()
		_, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
			Id:                        defaultAppID,
			Namespace:                 defaultNamespace,
			CertificateSigningRequest: []byte("-----BEGIN PRIVATE KEY-----\nMC4CAQBLAHBLAH\n-----END PRIVATE KEY-----"),
			TokenValidator:            sentrypbv1.SignCertificateRequest_INSECURE,
		})
		require.Error(t, err)

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, grpcStatus.Code())
		require.Contains(t, grpcStatus.Message(), "invalid certificate signing request")
	})
}
