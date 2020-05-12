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
	auth "github.com/dapr/dapr/pkg/runtime/security"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	grpc_go "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

const (
	certWatchInterval              = time.Second * 3
	renewWhenPercentagePassed      = 70
	defaultMaxConnectionAgeSeconds = 30
)

// Server implements the gRPC transport for a Dapr server.
type Server struct {
	logger             logger.Logger
	register           RegisterServerFn
	config             ServerConfig
	listener           net.Listener
	srv                *grpc_go.Server
	tracingSpec        config.TracingSpec
	authenticator      auth.Authenticator
	renewMutex         sync.Mutex
	signedCert         *auth.SignedCertificate
	tlsCert            tls.Certificate
	signedCertDuration time.Duration
	maxConnectionAge   *time.Duration
}

// RegisterServerFn is the function to register gRPC services.
type RegisterServerFn func(server *grpc_go.Server) error

// NewServer creates a new `Server` which delegates service registration to `register`.
func NewServer(logger logger.Logger,
	register RegisterServerFn,
	config ServerConfig,
	tracingSpec config.TracingSpec,
	authenticator auth.Authenticator) *Server {
	return &Server{
		logger:           logger,
		register:         register,
		config:           config,
		tracingSpec:      tracingSpec,
		authenticator:    authenticator,
		maxConnectionAge: getDefaultMaxAgeDuration(),
	}
}

func getDefaultMaxAgeDuration() *time.Duration {
	d := time.Second * defaultMaxConnectionAgeSeconds
	return &d
}

// StartNonBlocking starts a new server in a goroutine
func (s *Server) StartNonBlocking() error {
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
	err = s.register(server)
	if err != nil {
		return err
	}

	go func() {
		if err := server.Serve(lis); err != nil {
			s.logger.Fatalf("gRPC serve error: %v", err)
		}
	}()
	return nil
}

func (s *Server) generateWorkloadCert() error {
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

func (s *Server) getMiddlewareOptions() []grpc_go.ServerOption {
	opts := []grpc_go.ServerOption{}

	s.logger.Infof("enabled monitoring middleware.")
	unaryServerInterceptor := diag.SetTracingSpanContextGRPCMiddlewareUnary(s.tracingSpec)

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		unaryServerInterceptor = grpc_middleware.ChainUnaryServer(
			unaryServerInterceptor,
			diag.DefaultGRPCMonitoring.UnaryServerInterceptor(),
		)
	}

	opts = append(
		opts,
		grpc_go.StreamInterceptor(diag.SetTracingSpanContextGRPCMiddlewareStream(s.tracingSpec)),
		grpc_go.UnaryInterceptor(unaryServerInterceptor))

	return opts
}

func (s *Server) getGRPCServer() (*grpc_go.Server, error) {
	opts := s.getMiddlewareOptions()
	if s.maxConnectionAge != nil {
		opts = append(opts, grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionAge: *s.maxConnectionAge}))
	}

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

func (s *Server) startWorkloadCertRotation() {
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
