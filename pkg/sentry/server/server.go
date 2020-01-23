package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	pb "github.com/dapr/dapr/pkg/proto/sentry"
	"github.com/dapr/dapr/pkg/sentry/ca"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/pkg/sentry/csr"
	"github.com/golang/protobuf/ptypes"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	serverCertExpiryBuffer = time.Minute * 15
)

type CAServer interface {
	Run(port int, trustBundle ca.TrustRootBundler) error
	Shutdown()
}

type server struct {
	certificate *tls.Certificate
	certAuth    ca.CertificateAuthority
	srv         *grpc.Server
}

func NewCAServer(ca ca.CertificateAuthority) CAServer {
	return &server{
		certAuth: ca,
	}
}

// Run starts a secured gRPC server for the Sentry Certificate Authority.
// It enforces client side cert validation using the trust root cert.
func (s *server) Run(port int, trustBundler ca.TrustRootBundler) error {
	addr := fmt.Sprintf(":%v", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("could not listen on %s: %s", addr, err)
	}

	tlsOpt := s.tlsServerOption(trustBundler)
	s.srv = grpc.NewServer(tlsOpt)
	pb.RegisterCAServer(s.srv, s)

	if err := s.srv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve error: %s", err)
	}
	return nil
}

func (s *server) tlsServerOption(trustBundler ca.TrustRootBundler) grpc.ServerOption {
	cp := trustBundler.GetTrustAnchors()

	config := &tls.Config{
		ClientCAs: cp,
		// Require cert verification
		ClientAuth: tls.VerifyClientCertIfGiven,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if s.certificate == nil || needsRefresh(s.certificate, serverCertExpiryBuffer) {
				cert, err := s.getServerCertificate()
				if err != nil {
					log.Error(err)
					return nil, fmt.Errorf("failed to get TLS server certificate: %s", err)
				}
				s.certificate = cert
			}
			return s.certificate, nil
		},
	}
	return grpc.Creds(credentials.NewTLS(config))
}

func (s *server) getServerCertificate() (*tls.Certificate, error) {
	csrPem, pkPem, err := csr.GenerateCSR("", false)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	issuerExp := s.certAuth.GetCACertBundle().GetIssuerCertExpiry()
	serverCertTTL := issuerExp.Sub(now)

	resp, err := s.certAuth.SignCSR(csrPem, s.certAuth.GetCACertBundle().GetTrustDomain(), serverCertTTL, false)
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(resp.CertPEM, pkPem)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// SignCertificate handles CSR requests originating from Dapr sidecars.
// The method receives a request with an identity and initial cert and returns
// A signed certificate including the trust chain to the caller along with an expiry date.
func (s *server) SignCertificate(ctx context.Context, req *pb.SignCertificateRequest) (*pb.SignCertificateResponse, error) {
	csrPem := req.GetCertificateSigningRequest()
	csr, err := certs.ParsePemCSR(csrPem)
	if err != nil {
		err = fmt.Errorf("cannot parse certificate signing request pem: %s", err)
		log.Error(err)
		return nil, err
	}

	err = s.certAuth.ValidateCSR(csr)
	if err != nil {
		err = fmt.Errorf("error validating csr: %s", err)
		log.Error(err)
		return nil, err
	}

	signed, err := s.certAuth.SignCSR(csrPem, csr.Subject.CommonName, -1, false)
	if err != nil {
		err = fmt.Errorf("error signing csr: %s", err)
		log.Error(err)
		return nil, err
	}

	certs := signed.GetChain()
	if len(certs) == 0 {
		err = fmt.Errorf("insufficient data in certificate signing request, no certs signed")
		log.Error(err)
		return nil, err
	}

	expiry, err := ptypes.TimestampProto(signed.Certificate.NotAfter)
	if err != nil {
		return nil, fmt.Errorf("could not validate certificate validity: %s", err)
	}

	resp := &pb.SignCertificateResponse{
		WorkloadCertificate:    certs[0],
		TrustChainCertificates: certs[1:],
		ValidUntil:             expiry,
	}
	return resp, nil
}

func (s *server) Shutdown() {
	s.srv.Stop()
}

func needsRefresh(cert *tls.Certificate, expiryBuffer time.Duration) bool {
	leaf := cert.Leaf
	if leaf == nil {
		return true
	}

	// Check if the leaf certificate is about to expire.
	return leaf.NotAfter.Add(-serverCertExpiryBuffer).Before(time.Now())
}
