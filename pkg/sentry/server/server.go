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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	secpem "github.com/dapr/dapr/pkg/security/pem"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.server")

// Options is the configuration for the server.
type Options struct {
	// Port is the port that the server will listen on.
	Port int

	// Security is the security listening configuration for the server.
	Security security.Interface

	// Validator is the client authentication validator.
	Validator validator.Interface

	// CA is the certificate authority which signs client certificates.
	CA ca.Interface
}

// server is the gRPC server for the Sentry service.
type server struct {
	val validator.Interface
	ca  ca.Interface
}

// Start starts the server. Blocks until the context is cancelled.
func Start(ctx context.Context, opts Options) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", opts.Port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", opts.Port, err)
	}

	// No client auth because we auth based on the client SignCertificateRequest.
	srv := grpc.NewServer(opts.Security.GRPCServerOptionNoClientAuth())

	s := &server{
		val: opts.Validator,
		ca:  opts.CA,
	}
	sentryv1pb.RegisterCAServer(srv, s)

	errCh := make(chan error, 1)
	go func() {
		log.Infof("Running gRPC server on port %d", opts.Port)
		if err := svr.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		errCh <- nil
	}()

	<-ctx.Done()
	log.Info("Shutting down gRPC server")
	svr.GracefulStop()
	return <-errCh
}

// SignCertificate implements the SignCertificate gRPC method.
func (s *server) SignCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	monitoring.CertSignRequestReceived()
	resp, err := s.signCertificate(ctx, req)
	if err != nil {
		monitoring.CertSignFailed("sign")
		return nil, err
	}
	monitoring.CertSignSucceed()
	return resp, nil
}

func (s *server) signCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	trustDomain, err := s.val.Validate(ctx, req)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	der, _ := pem.Decode(req.GetCertificateSigningRequest())
	if der == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid certificate signing request")
	}
	csr, err := x509.ParseCertificateRequest(der.Bytes)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if csr.CheckSignature() != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signature")
	}

	// TODO: @joshvanl: before v1.11, daprd was matching on
	// `<app-id>.<namespace>.svc.cluster.local` DNS SAN name so without this,
	// daprd->daprd connections would fail. This is no longer the case since we
	// now match with SPIFFE URI SAN, but we need to keep this here for backwards
	// compatibility. Remove after v1.12.
	var dns []string
	switch {
	case req.Namespace == security.CurrentNamespace() && req.Id == "dapr-injector":
		dns = []string{fmt.Sprintf("dapr-sidecar-injector.%s.svc", req.Namespace)}
	case req.Namespace == security.CurrentNamespace() && req.Id == "dapr-operator":
		dns = []string{fmt.Sprintf("dapr-webhook.%s.svc", req.Namespace)}
	default:
		dns = []string{fmt.Sprintf("%s.%s.svc.cluster.local", req.Id, req.Namespace)}
	}

	chain, err := s.ca.SignIdentity(ctx, &ca.SignRequest{
		PublicKey:          csr.PublicKey,
		SignatureAlgorithm: csr.SignatureAlgorithm,
		TrustDomain:        trustDomain.String(),
		Namespace:          req.Namespace,
		AppID:              req.Id,
		DNS:                dns,
	})
	if err != nil {
		log.Errorf("Error signing identity: %v", err)
		return nil, status.Error(codes.Internal, "failed to sign certificate")
	}

	chainPEM, err := secpem.EncodeX509Chain(chain)
	if err != nil {
		log.Errorf("Error encoding certificate chain: %v", err)
		return nil, status.Error(codes.Internal, "failed to encode certificate chain")
	}

	return &sentryv1pb.SignCertificateResponse{
		WorkloadCertificate: chainPEM,
		// We only populate the trust chain and valid until for clients pre 1.11.
		// TODO: Remove fields in 1.12.
		TrustChainCertificates: [][]byte{s.ca.TrustAnchors()},
		ValidUntil:             timestamppb.New(chain[0].NotAfter),
	}, nil
}
