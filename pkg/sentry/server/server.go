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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/healthz"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/pkg/sentry/server/ca/jwt"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	secpem "github.com/dapr/kit/crypto/pem"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.server")

// Options is the configuration for the server.
type Options struct {
	// Port is the port that the server will listen on.
	Port int

	// ListenAddress is the address that the server will listen on.
	ListenAddress string

	// Security is the security Provider for the server.
	Security security.Provider

	// Validator are the client authentication validator.
	Validators map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator

	// Name of the default validator to use if the request doesn't specify one.
	DefaultValidator sentryv1pb.SignCertificateRequest_TokenValidator

	// CA is the certificate authority which signs client certificates.
	CA ca.Signer

	// Healthz is the healthz handler for the server.
	Healthz healthz.Healthz

	// JWTEnabled indicates whether JWT support is enabled
	JWTEnabled bool

	// JWTTTL is the time to live for the JWT token.
	JWTTTL time.Duration
}

// Server is the gRPC server for the Sentry service.
type Server struct {
	port             int
	listenAddress    string
	sec              security.Provider
	vals             map[sentryv1pb.SignCertificateRequest_TokenValidator]validator.Validator
	defaultValidator sentryv1pb.SignCertificateRequest_TokenValidator
	ca               ca.Signer
	htarget          healthz.Target
	jwtEnabled       bool
	jwtTTL           time.Duration
}

func New(opts Options) *Server {
	return &Server{
		port:             opts.Port,
		listenAddress:    opts.ListenAddress,
		sec:              opts.Security,
		vals:             opts.Validators,
		defaultValidator: opts.DefaultValidator,
		ca:               opts.CA,
		htarget:          opts.Healthz.AddTarget("sentry-server"),
		jwtEnabled:       opts.JWTEnabled,
		jwtTTL:           opts.JWTTTL,
	}
}

// Start starts the server. Blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	sec, err := s.sec.Handler(ctx)
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.listenAddress, s.port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.port, err)
	}

	// No client auth because we auth based on the client SignCertificateRequest.
	srv := grpc.NewServer(sec.GRPCServerOptionNoClientAuth())
	sentryv1pb.RegisterCAServer(srv, s)

	errCh := make(chan error, 1)
	go func() {
		log.Infof("Running gRPC server on port %d", s.port)
		if err := srv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		errCh <- nil
	}()

	s.htarget.Ready()

	<-ctx.Done()
	s.htarget.NotReady()
	log.Info("Shutting down gRPC server")
	srv.GracefulStop()
	return <-errCh
}

// SignCertificate implements the SignCertificate gRPC method.
func (s *Server) SignCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	monitoring.CertSignRequestReceived()
	resp, err := s.signCertificate(ctx, req)
	if err != nil {
		monitoring.CertSignFailed("sign")
		return nil, err
	}
	monitoring.CertSignSucceed()
	return resp, nil
}

func (s *Server) signCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
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

	res, err := s.vals[validator].Validate(ctx, req)
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

	var dns []string
	switch {
	case req.GetNamespace() == security.CurrentNamespace() && req.GetId() == "dapr-injector":
		dns = []string{fmt.Sprintf("dapr-sidecar-injector.%s.svc", req.GetNamespace())}
	case req.GetNamespace() == security.CurrentNamespace() && req.GetId() == "dapr-operator":
		dns = []string{fmt.Sprintf("dapr-webhook.%s.svc", req.GetNamespace())}
	case req.GetNamespace() == security.CurrentNamespace() && req.GetId() == "dapr-scheduler":
		dns = []string{
			fmt.Sprintf("dapr-scheduler-server-0.dapr-scheduler-server.%s.svc.cluster.local", req.GetNamespace()),
			fmt.Sprintf("dapr-scheduler-server-1.dapr-scheduler-server.%s.svc.cluster.local", req.GetNamespace()),
			fmt.Sprintf("dapr-scheduler-server-2.dapr-scheduler-server.%s.svc.cluster.local", req.GetNamespace()),
		}
	}

	chain, err := s.ca.SignIdentity(ctx, &ca.SignRequest{
		PublicKey:          csr.PublicKey,
		SignatureAlgorithm: csr.SignatureAlgorithm,
		TrustDomain:        res.TrustDomain.String(),
		Namespace:          namespace,
		AppID:              req.GetId(),
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

	log.Debugf("Successfully signed certificate for %s/%s", namespace, req.GetId())

	var jwtToken *wrapperspb.StringValue
	if s.jwtEnabled {
		audiences := append([]string{res.TrustDomain.String()}, req.GetJwtAudiences()...) // Default audience is the trust domain

		// Generate a JWT with the same identity
		tkn, err := s.ca.Generate(ctx, &jwt.Request{
			TrustDomain: res.TrustDomain,
			Audiences:   audiences,
			Namespace:   req.GetNamespace(),
			AppID:       req.GetId(),
			TTL:         s.jwtTTL,
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate JWT: %v", err)
		}
		if tkn == "" {
			return nil, status.Error(codes.Internal, "failed to generate JWT: empty token")
		}
		jwtToken = wrapperspb.String(tkn)
	}

	return &sentryv1pb.SignCertificateResponse{
		WorkloadCertificate:    chainPEM,
		TrustChainCertificates: [][]byte{s.ca.TrustAnchors()},
		ValidUntil:             timestamppb.New(chain[0].NotAfter),
		Jwt:                    jwtToken,
	}, nil
}
