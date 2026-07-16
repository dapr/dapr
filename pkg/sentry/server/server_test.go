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

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/healthz"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	cafake "github.com/dapr/dapr/pkg/sentry/server/ca/fake"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	validatorfake "github.com/dapr/dapr/pkg/sentry/server/validator/fake"
)

func TestRun(t *testing.T) {
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, pk)
	require.NoError(t, err)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2023, 1, 1, 1, 1, 1, 0, time.UTC),
	}
	crt, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pk.Public(), pk)
	require.NoError(t, err)
	crtX509, err := x509.ParseCertificate(crt)
	require.NoError(t, err)
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crt})

	tests := map[string]struct {
		sec *securityfake.Fake
		val validator.Validator
		ca  ca.Signer

		req     *sentryv1pb.SignCertificateRequest
		expResp *sentryv1pb.SignCertificateResponse
		expErr  bool
		expCode codes.Code
	}{
		"request where signing returns bad certificate chain should error": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("test"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				return []*x509.Certificate{}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-id",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "my-namespace",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: nil,
			expErr:  true,
			expCode: codes.Internal,
		},
		"request which signing cert should error": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("test"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				return nil, errors.New("signing error")
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-id",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "my-namespace",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: nil,
			expErr:  true,
			expCode: codes.Internal,
		},
		"request with a bad csr should fail": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("my-trust-domain"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				return []*x509.Certificate{crtX509}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-id",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "my-namespace",
				CertificateSigningRequest: []byte("bad csr"),
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: nil,
			expErr:  true,
			expCode: codes.InvalidArgument,
		},
		"request which fails validation should return error": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{}, errors.New("validation error")
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				return []*x509.Certificate{crtX509}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-id",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "my-namespace",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: nil,
			expErr:  true,
			expCode: codes.PermissionDenied,
		},
		"valid request should return valid response": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("my-trust-domain"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				return []*x509.Certificate{crtX509}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-id",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "my-namespace",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: &sentryv1pb.SignCertificateResponse{
				WorkloadCertificate:    crtPEM,
				TrustChainCertificates: [][]byte{[]byte("my-trust-anchors")},
				ValidUntil:             timestamppb.New(time.Date(2023, 1, 1, 1, 1, 1, 0, time.UTC)),
			},
			expErr:  false,
			expCode: codes.OK,
		},
		"if request is for injector, expect injector dns name": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("my-trust-domain"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				assert.Equal(t, []string{"dapr-sidecar-injector.default.svc"}, req.DNS)
				return []*x509.Certificate{crtX509}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "dapr-injector",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "default",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: &sentryv1pb.SignCertificateResponse{
				WorkloadCertificate:    crtPEM,
				TrustChainCertificates: [][]byte{[]byte("my-trust-anchors")},
				ValidUntil:             timestamppb.New(time.Date(2023, 1, 1, 1, 1, 1, 0, time.UTC)),
			},
			expErr:  false,
			expCode: codes.OK,
		},
		"if request is for operator, expect operator dns name": {
			sec: securityfake.New().WithGRPCServerOptionNoClientAuthFn(func() grpc.ServerOption {
				return grpc.Creds(insecure.NewCredentials())
			}),
			val: validatorfake.New().WithValidateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
				return validator.ValidateResult{
					TrustDomain: spiffeid.RequireTrustDomainFromString("my-trust-domain"),
				}, nil
			}),
			ca: cafake.New().WithSignIdentity(func(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
				assert.Equal(t, []string{"dapr-webhook.default.svc"}, req.DNS)
				return []*x509.Certificate{crtX509}, nil
			}).WithTrustAnchors(func() []byte {
				return []byte("my-trust-anchors")
			}),
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "dapr-operator",
				Token:                     "my-token",
				TrustDomain:               "my-trust-domain",
				Namespace:                 "default",
				CertificateSigningRequest: csrPEM,
				TokenValidator:            sentryv1pb.SignCertificateRequest_TokenValidator(-1),
			},
			expResp: &sentryv1pb.SignCertificateResponse{
				WorkloadCertificate:    crtPEM,
				TrustChainCertificates: [][]byte{[]byte("my-trust-anchors")},
				ValidUntil:             timestamppb.New(time.Date(2023, 1, 1, 1, 1, 1, 0, time.UTC)),
			},
			expErr:  false,
			expCode: codes.OK,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			port, err := freeport.GetFreePort()
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			opts := Options{
				Port:     port,
				Security: test.sec,
				Validators: map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator{
					// This is an invalid validator that is just used for tests
					-1: test.val,
				},
				CA:      test.ca,
				Healthz: healthz.New(),
			}

			serverClosed := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				select {
				case <-serverClosed:
				case <-time.After(3 * time.Second):
					assert.Fail(t, "server did not close in time")
				}
			})

			go func() {
				defer close(serverClosed)
				assert.NoError(t, New(opts).Start(ctx))
			}()

			require.Eventually(t, func() bool {
				var conn net.Conn
				conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
				if err == nil {
					conn.Close()
				}
				return err == nil
			}, time.Second, 10*time.Millisecond)

			conn, err := grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials())) //nolint:staticcheck
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, conn.Close())
			})

			client := sentryv1pb.NewCAClient(conn)

			resp, err := client.SignCertificate(ctx, test.req)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			assert.Equalf(t, test.expCode, status.Code(err), "unexpected error code: %v", err)

			require.Equalf(t, test.expResp == nil, resp == nil, "expected response to be nil: %v", resp)
			if test.expResp != nil {
				assert.Equal(t, test.expResp.GetTrustChainCertificates(), resp.GetTrustChainCertificates())
				assert.Equal(t, test.expResp.GetValidUntil(), resp.GetValidUntil())
				assert.Equal(t, test.expResp.GetWorkloadCertificate(), resp.GetWorkloadCertificate())
			}
		})
	}
}
