// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	dapr_pb "github.com/dapr/dapr/pkg/proto/dapr"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	log "github.com/sirupsen/logrus"
	grpc_go "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	certWatchInterval         = time.Second * 5
	renewWhenPercentagePassed = 70
)

// Server is an interface for the dapr gRPC server
type Server interface {
	StartNonBlocking() error
}

type server struct {
	api           API
	config        ServerConfig
	tracingSpec   config.TracingSpec
	authenticator auth.Authenticator
	listener      net.Listener
	srv           *grpc_go.Server
}

// NewServer returns a new gRPC server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec, authenticator auth.Authenticator) Server {
	return &server{
		api:           api,
		config:        config,
		tracingSpec:   tracingSpec,
		authenticator: authenticator,
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

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()
	return nil
}

func (s *server) stop() error {
	s.srv.Stop()
	return s.listener.Close()
}

func (s *server) getGRPCServer() (*grpc_go.Server, error) {
	opts := []grpc_go.ServerOption{}

	if s.tracingSpec.Enabled {
		opts = append(opts, grpc_go.StreamInterceptor(diag.TracingGRPCMiddleware(s.tracingSpec)))
		opts = append(opts, grpc_go.UnaryInterceptor(diag.TracingGRPCMiddlewareUnary(s.tracingSpec)))
	}

	if s.authenticator != nil {
		log.Info("sending workload csr request to sentry")
		signedCert, err := s.authenticator.CreateSignedWorkloadCert(s.config.DaprID)
		if err != nil {
			return nil, fmt.Errorf("error from authenticator CreateSignedWorkloadCert: %s", err)
		}

		tlsCert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
		if err != nil {
			return nil, fmt.Errorf("error creating x509 Key Pair: %s", err)
		}

		caCertPool := signedCert.TrustChain
		tlsConfig := tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			ClientCAs:    caCertPool,
			ClientAuth:   tls.VerifyClientCertIfGiven,
		}
		ta := credentials.NewTLS(&tlsConfig)

		log.Info("certificate signed successfully")

		opts = append(opts, grpc_go.Creds(ta))
		go s.startWorkloadCertRotation(signedCert.Expiry)
	}
	return grpc_go.NewServer(opts...), nil
}

func (s *server) startWorkloadCertRotation(expiry time.Time) {
	log.Infof("starting workload cert expiry watcher. current cert expires on: %s", expiry.String())

	certDuration := expiry.Sub(time.Now().UTC())

	ticker := time.NewTicker(certWatchInterval)
	done := make(chan bool)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			renew := shouldRenewCert(expiry, certDuration)
			if renew {
				log.Info("renewing certificate: requesting new cert and restarting gRPC server")
				err := s.stop()
				if err != nil {
					log.Errorf("error stopping server: %s", err)
				}

				err = s.StartNonBlocking()
				if err != nil {
					log.Errorf("error starting server: %s", err)
				} else {
					close(done)
				}
			}
		}
	}
}

func shouldRenewCert(endDate time.Time, certDuration time.Duration) bool {
	d := endDate.Sub(time.Now().UTC())
	eS := certDuration.Seconds()
	dS := d.Seconds()

	percentagePassed := ((eS - dS) * 100) / eS
	return percentagePassed >= renewWhenPercentagePassed
}
