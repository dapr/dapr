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
	grpc_go "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	certWatchInterval         = time.Second * 3
	renewWhenPercentagePassed = 70
	apiServer                 = "apiServer"
	internalServer            = "internalServer"
)

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
	kind               string
	logger             logger.Logger
}

var apiServerLogger = logger.NewLogger("dapr.runtime.grpc.api")
var internalServerLogger = logger.NewLogger("dapr.runtime.grpc.internal")

// NewAPIServer returns a new user facing gRPC API server
func NewAPIServer(api API, config ServerConfig, tracingSpec config.TracingSpec) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
		kind:        apiServer,
		logger:      apiServerLogger,
	}
}

// NewInternalServer returns a new gRPC server for Dapr to Dapr communications
func NewInternalServer(api API, config ServerConfig, tracingSpec config.TracingSpec, authenticator auth.Authenticator) Server {
	return &server{
		api:           api,
		config:        config,
		tracingSpec:   tracingSpec,
		authenticator: authenticator,
		renewMutex:    &sync.Mutex{},
		kind:          internalServer,
		logger:        internalServerLogger,
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

	if s.kind == internalServer {
		daprinternal_pb.RegisterDaprInternalServer(server, s.api)
	} else if s.kind == apiServer {
		dapr_pb.RegisterDaprServer(server, s.api)
	}
	go func() {
		if err := server.Serve(lis); err != nil {
			s.logger.Fatalf("gRPC serve error: %v", err)
		}
	}()
	return nil
}

func (s *server) generateWorkloadCert() error {
	s.logger.Info("sending workload csr request to sentry")
	signedCert, err := s.authenticator.CreateSignedWorkloadCert(s.config.AppID)
	if err != nil {
		return fmt.Errorf("error from authenticator CreateSignedWorkloadCert: %s", err)
	}
	s.logger.Info("certificate signed successfully")

	tlsCert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
	if err != nil {
		return fmt.Errorf("error creating x509 Key Pair: %s", err)
	}

	s.signedCert = signedCert
	s.tlsCert = tlsCert
	s.signedCertDuration = signedCert.Expiry.Sub(time.Now().UTC())
	return nil
}

func (s *server) getMiddlewareOptions() []grpc_go.ServerOption {
	opts := []grpc_go.ServerOption{}

	s.logger.Infof("enabled tracing grpc middleware")
	opts = append(
		opts,
		grpc_go.StreamInterceptor(diag.TracingGRPCMiddlewareStream(s.tracingSpec)),
		grpc_go.UnaryInterceptor(diag.TracingGRPCMiddlewareUnary(s.tracingSpec)))

	s.logger.Infof("enabled metrics grpc middleware")
	opts = append(opts, grpc_go.StatsHandler(diag.DefaultGRPCMonitoring.ServerStatsHandler))

	return opts
}

func (s *server) getGRPCServer() (*grpc_go.Server, error) {
	opts := s.getMiddlewareOptions()

	if s.authenticator != nil {
		err := s.generateWorkloadCert()
		if err != nil {
			return nil, err
		}

		tlsConfig := tls.Config{
			ClientCAs:  s.signedCert.TrustChain,
			ClientAuth: tls.RequireAndVerifyClientCert,
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
	s.logger.Infof("starting workload cert expiry watcher. current cert expires on: %s", s.signedCert.Expiry.String())

	ticker := time.NewTicker(certWatchInterval)

	for range ticker.C {
		s.renewMutex.Lock()
		renew := shouldRenewCert(s.signedCert.Expiry, s.signedCertDuration)
		if renew {
			s.logger.Info("renewing certificate: requesting new cert and restarting gRPC server")

			err := s.generateWorkloadCert()
			if err != nil {
				s.logger.Errorf("error starting server: %s", err)
			}
			diag.DefaultMonitoring.MTLSWorkLoadCertRotationCompleted()
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
