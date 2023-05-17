package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/timestamppb"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/csr"
	"github.com/dapr/dapr/pkg/sentry/identity"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/kit/logger"
)

const (
	serverCertExpiryBuffer = time.Minute * 15
)

var log = logger.NewLogger("dapr.sentry.server")

// CAServer is an interface for the Certificate Authority server.
type CAServer interface {
	Run(ctx context.Context, port int, trustBundle ca.TrustRootBundler) error
}

type server struct {
	certificate *tls.Certificate
	certAuth    ca.CertificateAuthority
	srv         *grpc.Server
	validator   identity.Validator
	metrics     *diag.Metrics
}

// NewCAServer returns a new CA Server running a gRPC server.
func NewCAServer(ca ca.CertificateAuthority, validator identity.Validator, metrics *diag.Metrics) CAServer {
	return &server{
		certAuth:  ca,
		validator: validator,
		metrics:   metrics,
	}
}

// Run starts a secured gRPC server for the Sentry Certificate Authority.
// It enforces client side cert validation using the trust root cert.
func (s *server) Run(ctx context.Context, port int, trustBundler ca.TrustRootBundler) error {
	addr := fmt.Sprintf(":%d", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("could not listen on %s: %w", addr, err)
	}

	tlsOpt := s.tlsServerOption(trustBundler)
	s.srv = grpc.NewServer(tlsOpt)
	sentryv1pb.RegisterCAServer(s.srv, s)

	serveErr := make(chan error, 1)
	log.Infof("sentry server is listening on %s", lis.Addr())
	go func() {
		if err := s.srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serveErr <- fmt.Errorf("grpc serve error: %w", err)
			return
		}
		serveErr <- nil
	}()

	<-ctx.Done()
	log.Info("sentry server is shutting down")
	s.srv.GracefulStop()
	return <-serveErr
}

func (s *server) tlsServerOption(trustBundler ca.TrustRootBundler) grpc.ServerOption {
	cp := trustBundler.GetTrustAnchors()

	config := &tls.Config{
		ClientCAs: cp,
		// Require cert verification
		ClientAuth: tls.RequireAndVerifyClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if s.certificate == nil || needsRefresh(s.certificate, serverCertExpiryBuffer) {
				cert, err := s.getServerCertificate()
				if err != nil {
					monitoring.ServerCertIssueFailed(s.metrics, "server_cert")
					log.Error(err)
					return nil, fmt.Errorf("failed to get TLS server certificate: %w", err)
				}
				s.certificate = cert
			}
			return s.certificate, nil
		},
		MinVersion: tls.VersionTLS12,
	}
	return grpc.Creds(credentials.NewTLS(config))
}

func (s *server) getServerCertificate() (*tls.Certificate, error) {
	csrPem, pkPem, err := csr.GenerateCSR("", false)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	issuerExp := s.certAuth.GetCACertBundle().GetIssuerCertExpiry()
	if issuerExp == nil {
		return nil, errors.New("could not find expiration in issuer certificate")
	}
	serverCertTTL := issuerExp.Sub(now)

	resp, err := s.certAuth.SignCSR(csrPem, s.certAuth.GetCACertBundle().GetTrustDomain(), nil, serverCertTTL, false)
	if err != nil {
		return nil, err
	}

	certPem := resp.CertPEM
	certPem = append(certPem, s.certAuth.GetCACertBundle().GetIssuerCertPem()...)
	if rootCertPem := s.certAuth.GetCACertBundle().GetRootCertPem(); len(rootCertPem) > 0 {
		certPem = append(certPem, rootCertPem...)
	}

	cert, err := tls.X509KeyPair(certPem, pkPem)
	if err != nil {
		return nil, err
	}

	return &cert, nil
}

// SignCertificate handles CSR requests originating from Dapr sidecars.
// The method receives a request with an identity and initial cert and returns
// A signed certificate including the trust chain to the caller along with an expiry date.
func (s *server) SignCertificate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
	monitoring.CertSignRequestReceived(s.metrics)

	csrPem := req.GetCertificateSigningRequest()

	csr, err := certs.ParsePemCSR(csrPem)
	if err != nil {
		err = fmt.Errorf("cannot parse certificate signing request pem: %w", err)
		log.Error(err)
		monitoring.CertSignFailed(s.metrics, "cert_parse")
		return nil, err
	}

	err = s.certAuth.ValidateCSR(csr)
	if err != nil {
		err = fmt.Errorf("error validating csr: %w", err)
		log.Error(err)
		monitoring.CertSignFailed(s.metrics, "cert_validation")
		return nil, err
	}

	err = s.validator.Validate(req.GetId(), req.GetToken(), req.GetNamespace())
	if err != nil {
		err = fmt.Errorf("error validating requester identity: %w", err)
		log.Error(err)
		monitoring.CertSignFailed(s.metrics, "req_id_validation")
		return nil, err
	}

	identity := identity.NewBundle(csr.Subject.CommonName, req.GetNamespace(), req.GetTrustDomain())
	signed, err := s.certAuth.SignCSR(csrPem, csr.Subject.CommonName, identity, -1, false)
	if err != nil {
		err = fmt.Errorf("error signing csr: %w", err)
		log.Error(err)
		monitoring.CertSignFailed(s.metrics, "cert_sign")
		return nil, err
	}

	certPem := signed.CertPEM
	issuerCert := s.certAuth.GetCACertBundle().GetIssuerCertPem()
	rootCert := s.certAuth.GetCACertBundle().GetRootCertPem()

	certPem = append(certPem, issuerCert...)
	if len(rootCert) > 0 {
		certPem = append(certPem, rootCert...)
	}

	if len(certPem) == 0 {
		err = errors.New("insufficient data in certificate signing request, no certs signed")
		log.Error(err)
		monitoring.CertSignFailed(s.metrics, "insufficient_data")
		return nil, err
	}

	expiry := timestamppb.New(signed.Certificate.NotAfter)
	if err = expiry.CheckValid(); err != nil {
		return nil, fmt.Errorf("could not validate certificate validity: %w", err)
	}

	resp := &sentryv1pb.SignCertificateResponse{
		WorkloadCertificate:    certPem,
		TrustChainCertificates: [][]byte{issuerCert, rootCert},
		ValidUntil:             expiry,
	}

	monitoring.CertSignSucceed(s.metrics)

	return resp, nil
}

func needsRefresh(cert *tls.Certificate, expiryBuffer time.Duration) bool {
	leaf := cert.Leaf
	if leaf == nil {
		return true
	}

	// Check if the leaf certificate is about to expire.
	return leaf.NotAfter.Add(-serverCertExpiryBuffer).Before(time.Now().UTC())
}
