// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	dapr_pb "github.com/dapr/dapr/pkg/proto/dapr"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	grpc_go "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	certWatchInterval         = time.Second * 3
	renewWhenPercentagePassed = 70
)

var log = logger.NewLogger("dapr.runtime.grpc")

// Server is an interface for the dapr gRPC server
type Server interface {
	StartNonBlocking() error
}

type server struct {
	api                API
	config             ServerConfig
	tracingSpec        config.TracingSpec
	authenticator      auth.Authenticator
	listener           net.Listener
	srv                *grpc_go.Server
	renewMutex         *sync.Mutex
	signedCert         *auth.SignedCertificate
	tlsCert            tls.Certificate
	signedCertDuration time.Duration
}

// NewServer returns a new gRPC server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec, authenticator auth.Authenticator) Server {
	return &server{
		api:           api,
		config:        config,
		tracingSpec:   tracingSpec,
		authenticator: authenticator,
		renewMutex:    &sync.Mutex{},
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%v", s.config.Port))
	if err != nil {
		return err
	}
	s.listener = lis

	server, err := s.getGRPCServer()
	if err != nil {
		return err
	}
	s.srv = server

	daprinternal_pb.RegisterDaprInternalServer(server, s.api)
	dapr_pb.RegisterDaprServer(server, s.api)

	if s.config.EnableMetrics {
		grpc_prometheus.Register(s.srv)
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()
	return nil
}

func (s *server) generateWorkloadCert() error {
	log.Info("sending workload csr request to sentry")
	signedCert, err := s.authenticator.CreateSignedWorkloadCert(s.config.AppID)
	if err != nil {
		return fmt.Errorf("error from authenticator CreateSignedWorkloadCert: %s", err)
	}
	log.Info("certificate signed successfully")

	tlsCert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
	if err != nil {
		return fmt.Errorf("error creating x509 Key Pair: %s", err)
	}

	s.signedCert = signedCert
	s.tlsCert = tlsCert
	s.signedCertDuration = signedCert.Expiry.Sub(time.Now().UTC())
	return nil
}

func (s *server) getGRPCServer() (*grpc_go.Server, error) {
	opts := []grpc_go.ServerOption{}

	if s.tracingSpec.Enabled {
		log.Infof("enabled tracing grpc middleware")
		opts = append(opts, grpc_go.StreamInterceptor(diag.TracingGRPCMiddlewareStream(s.tracingSpec)), grpc_go.UnaryInterceptor(diag.TracingGRPCMiddlewareUnary(s.tracingSpec)))
	}
	if s.config.EnableMetrics {
		log.Infof("enabled metrics grpc middleware")
		opts = append(opts, grpc_go.StreamInterceptor(diag.MetricsGRPCMiddlewareStream()), grpc_go.UnaryInterceptor(diag.MetricsGRPCMiddlewareUnary()))
	}

	if s.authenticator != nil {
		err := s.generateWorkloadCert()
		if err != nil {
			return nil, err
		}

		tlsConfig := tls.Config{
			ClientCAs:  s.signedCert.TrustChain,
			ClientAuth: tls.VerifyClientCertIfGiven,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return &s.tlsCert, nil
			},
		}
		ta := credentials.NewTLS(&tlsConfig)

		opts = append(opts, grpc_go.Creds(ta))
		go s.startWorkloadCertRotation()
	}
	return grpc_go.NewServer(opts...), nil
}

func (s *server) startWorkloadCertRotation() {
	log.Infof("starting workload cert expiry watcher. current cert expires on: %s", s.signedCert.Expiry.String())

	ticker := time.NewTicker(certWatchInterval)

	for range ticker.C {
		s.renewMutex.Lock()
		renew := shouldRenewCert(s.signedCert.Expiry, s.signedCertDuration)
		if renew {
			log.Info("renewing certificate: requesting new cert and restarting gRPC server")

			err := s.generateWorkloadCert()
			if err != nil {
				log.Errorf("error starting server: %s", err)
			}
		}
		s.renewMutex.Unlock()
	}
}

func shouldRenewCert(certExpiryDate time.Time, certDuration time.Duration) bool {
	expiresIn := certExpiryDate.Sub(time.Now().UTC())
	expiresInSeconds := expiresIn.Seconds()
	certDurationSeconds := certDuration.Seconds()

	percentagePassed := 100 - ((expiresInSeconds / certDurationSeconds) * 100)
	return percentagePassed >= renewWhenPercentagePassed
}
