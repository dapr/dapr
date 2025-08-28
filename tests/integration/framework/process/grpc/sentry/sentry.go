/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sentry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

type Option func(*options)

type Sentry struct {
	grpc   *procgrpc.GRPC
	bundle bundle.Bundle
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    nil,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: "integration.test.dapr.io",
	})
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafCert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Second * 20),
		URIs: []*url.URL{
			spiffeid.RequireFromString("spiffe://localhost/ns/default/dapr-sentry").URL(),
		},
	}
	leafCertDer, err := x509.CreateCertificate(rand.Reader, leafCert, x509bundle.IssChain[0], &leafKey.PublicKey, x509bundle.IssKey)
	require.NoError(t, err)
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{leafCertDer, x509bundle.IssChain[0].Raw},
				PrivateKey:  leafKey,
			},
		},
	}

	s := &Sentry{
		bundle: bundle.Bundle{
			X509: x509bundle,
			JWT:  jwtbundle,
		},
	}

	opts := options{
		signCertificateFn: func(context.Context, *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
			return nil, nil
		},
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	s.grpc = procgrpc.New(t,
		procgrpc.WithServerOption(func(t *testing.T, ctx context.Context) grpc.ServerOption {
			return grpc.Creds(credentials.NewTLS(tlsConfig))
		}),
		procgrpc.WithRegister(
			func(s *grpc.Server) {
				srv := &server{
					signCertificateFn: opts.signCertificateFn,
				}
				sentryv1pb.RegisterCAServer(s, srv)
			},
		))

	return s
}

func (s *Sentry) Cleanup(t *testing.T) {
	s.grpc.Cleanup(t)
}

func (s *Sentry) Run(t *testing.T, ctx context.Context) {
	s.grpc.Run(t, ctx)
}

func (s *Sentry) Bundle() bundle.Bundle {
	return s.bundle
}

func (s *Sentry) Address(t *testing.T) string {
	return s.grpc.Address(t)
}
