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

package insecure

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
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

	m.proc.WaitUntilRunning(t, parentCtx)

	// Get a CSR for the app "myapp"
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, new(x509.CertificateRequest), pk)
	require.NoError(t, err)
	defaultAppCSR := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})

	// Connect to Sentry
	conn := m.proc.DialGRPC(t, parentCtx, "spiffe://localhost/ns/default/dapr-sentry")
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
		require.NotEmpty(t, res.GetWorkloadCertificate())

		validateCertificateResponse(t, res, m.proc.CABundle(), defaultAppSPIFFEID)
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
		require.NotEmpty(t, res.GetWorkloadCertificate())

		validateCertificateResponse(t, res, m.proc.CABundle(), defaultAppSPIFFEID)
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

func validateCertificateResponse(t *testing.T, res *sentrypbv1.SignCertificateResponse, sentryBundle bundle.Bundle, expectSPIFFEID string) {
	t.Helper()

	require.NotEmpty(t, res.GetWorkloadCertificate())

	rest := res.GetWorkloadCertificate()

	// First block should contain the issued workload certificate
	var block *pem.Block
	block, rest = pem.Decode(rest)
	require.NotEmpty(t, block)
	require.Equal(t, "CERTIFICATE", block.Type)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	certURIs := make([]string, len(cert.URIs))
	for i, v := range cert.URIs {
		certURIs[i] = v.String()
	}
	assert.Equal(t, []string{expectSPIFFEID}, certURIs)
	assert.Empty(t, cert.DNSNames)
	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)

	// Second block should contain the Sentry CA certificate
	block, rest = pem.Decode(rest)
	require.Empty(t, rest)
	require.NotEmpty(t, block)
	require.Equal(t, "CERTIFICATE", block.Type)

	cert, err = x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	assert.Empty(t, cert.DNSNames)
}
