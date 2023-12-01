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

	// Security is the security handler for the server.
	Security security.Handler

	// Validator are the client authentication validator.
	Validators map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator

	// Name of the default validator to use if the request doesn't specify one.
	DefaultValidator sentryv1pb.SignCertificateRequest_TokenValidator

	// CA is the certificate authority which signs client certificates.
	CA ca.Signer
}

// server is the gRPC server for the Sentry service.
type server struct {
	vals             map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator
	defaultValidator sentryv1pb.SignCertificateRequest_TokenValidator
	ca               ca.Signer
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
		vals:             opts.Validators,
		defaultValidator: opts.DefaultValidator,
		ca:               opts.CA,
	}
	sentryv1pb.RegisterCAServer(srv, s)

	errCh := make(chan error, 1)
	go func() {
		log.Infof("Running gRPC server on port %d", opts.Port)
		if err := srv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		errCh <- nil
	}()

	<-ctx.Done()
	log.Info("Shutting down gRPC server")
	srv.GracefulStop()
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
	validator := s.defaultValidator
	if req.GetTokenValidator() != sentryv1pb.SignCertificateRequest_UNKNOWN && req.GetTokenValidator().String() != "" {
		validator = req.GetTokenValidator()
	}
	namespace := req.GetNamespace()
	if validator == sentryv1pb.SignCertificateRequest_UNKNOWN {
		log.Debugf("Validator '%s' is not known for %s/%s", validator.String(), namespace, req.GetId())
		return nil, status.Error(codes.InvalidArgument, "a validator name must be specified in this environment")
	}
	if _, ok := s.vals[validator]; !ok {
		log.Debugf("Validator '%s' is not enabled for %s/%s", validator.String(), namespace, req.GetId())
		return nil, status.Error(codes.InvalidArgument, "the requested validator is not enabled")
	}

	log.Debugf("Processing SignCertificate request for %s/%s (validator: %s)", namespace, req.GetId(), validator.String())

	trustDomain, overrideDuration, err := s.vals[validator].Validate(ctx, req)
	if err != nil {
		log.Debugf("Failed to validate request for %s/%s: %s", namespace, req.GetId(), err)
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}

	der, _ := pem.Decode(req.GetCertificateSigningRequest())
	if der == nil {
		log.Debugf("Invalid CSR: PEM block is nil for %s/%s", namespace, req.GetId())
		return nil, status.Error(codes.InvalidArgument, "invalid certificate signing request")
	}
	// TODO: @joshvanl: Before v1.12, daprd was sending CSRs with the PEM block type "CERTIFICATE"
	// After 1.14, allow only "CERTIFICATE REQUEST"
	if der.Type != "CERTIFICATE REQUEST" && der.Type != "CERTIFICATE" {
		log.Debugf("Invalid CSR: PEM block type is invalid for %s/%s: %s", namespace, req.GetId(), der.Type)
		return nil, status.Error(codes.InvalidArgument, "invalid certificate signing request")
	}
	csr, err := x509.ParseCertificateRequest(der.Bytes)
	if err != nil {
		log.Debugf("Failed to parse CSR for %s/%s: %v", namespace, req.GetId(), err)
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse certificate signing request: %v", err)
	}

	if csr.CheckSignature() != nil {
		log.Debugf("Invalid CSR: invalid signature for %s/%s", namespace, req.GetId())
		return nil, status.Error(codes.InvalidArgument, "invalid signature")
	}

	// TODO: @joshvanl: before v1.12, daprd was matching on
	// `<app-id>.<namespace>.svc.cluster.local` DNS SAN name so without this,
	// daprd->daprd connections would fail. This is no longer the case since we
	// now match with SPIFFE URI SAN, but we need to keep this here for backwards
	// compatibility. Remove after v1.14.
	var dns []string
	switch {
	case namespace == security.CurrentNamespace() && req.GetId() == "dapr-injector":
		dns = []string{fmt.Sprintf("dapr-sidecar-injector.%s.svc", namespace)}
	case namespace == security.CurrentNamespace() && req.GetId() == "dapr-operator":
		// TODO: @joshvanl: before v1.12, daprd was matching on the operator server
		// having `cluster.local` as a DNS SAN name. Remove after v1.13.
		dns = []string{"cluster.local", fmt.Sprintf("dapr-webhook.%s.svc", namespace)}
	case namespace == security.CurrentNamespace() && req.GetId() == "dapr-placement":
		dns = []string{"cluster.local"}
	default:
		dns = []string{fmt.Sprintf("%s.%s.svc.cluster.local", req.GetId(), namespace)}
	}

	chain, err := s.ca.SignIdentity(ctx, &ca.SignRequest{
		PublicKey:          csr.PublicKey,
		SignatureAlgorithm: csr.SignatureAlgorithm,
		TrustDomain:        trustDomain.String(),
		Namespace:          namespace,
		AppID:              req.GetId(),
		DNS:                dns,
	}, overrideDuration)
	if err != nil {
		log.Errorf("Error signing identity: %v", err)
		return nil, status.Error(codes.Internal, "failed to sign certificate")
	}

	chainPEM, err := secpem.EncodeX509Chain(chain)
	if err != nil {
		log.Errorf("Error encoding certificate chain: %v", err)
		return nil, status.Error(codes.Internal, "failed to encode certificate chain")
	}

	log.Debugf("Successfully signed certificate for %s/%s", namespace, req.GetId())

	return &sentryv1pb.SignCertificateResponse{
		WorkloadCertificate: chainPEM,
		// We only populate the trust chain and valid until for clients pre-1.12.
		// TODO: Remove fields in 1.14.
		TrustChainCertificates: [][]byte{s.ca.TrustAnchors()},
		ValidUntil:             timestamppb.New(chain[0].NotAfter),
	}, nil
}
