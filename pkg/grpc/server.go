/*
Copyright 2021 The Dapr Authors
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

package grpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcGo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/grpc/metadata"
	"github.com/dapr/dapr/pkg/messaging"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

const (
	certWatchInterval              = time.Second * 3
	renewWhenPercentagePassed      = 70
	apiServer                      = "apiServer"
	internalServer                 = "internalServer"
	defaultMaxConnectionAgeSeconds = 30
)

// Server is an interface for the dapr gRPC server.
type Server interface {
	io.Closer
	StartNonBlocking() error
}

type server struct {
	api                API
	config             ServerConfig
	tracingSpec        config.TracingSpec
	metricSpec         config.MetricSpec
	authenticator      auth.Authenticator
	servers            []*grpcGo.Server
	renewMutex         sync.Mutex
	signedCert         *auth.SignedCertificate
	tlsCert            tls.Certificate
	signedCertDuration time.Duration
	kind               string
	logger             logger.Logger
	infoLogger         logger.Logger
	maxConnectionAge   *time.Duration
	authToken          string
	apiSpec            config.APISpec
	proxy              messaging.Proxy
	workflowEngine     *wfengine.WorkflowEngine
}

var (
	apiServerLogger      = logger.NewLogger("dapr.runtime.grpc.api")
	apiServerInfoLogger  = logger.NewLogger("dapr.runtime.grpc.api-info")
	internalServerLogger = logger.NewLogger("dapr.runtime.grpc.internal")
)

// NewAPIServer returns a new user facing gRPC API server.
func NewAPIServer(api API, config ServerConfig, tracingSpec config.TracingSpec, metricSpec config.MetricSpec, apiSpec config.APISpec, proxy messaging.Proxy, workflowEngine *wfengine.WorkflowEngine) Server {
	apiServerInfoLogger.SetOutputLevel(logger.LogLevel("info"))
	return &server{
		api:            api,
		config:         config,
		tracingSpec:    tracingSpec,
		metricSpec:     metricSpec,
		kind:           apiServer,
		logger:         apiServerLogger,
		infoLogger:     apiServerInfoLogger,
		authToken:      auth.GetAPIToken(),
		apiSpec:        apiSpec,
		proxy:          proxy,
		workflowEngine: workflowEngine,
	}
}

// NewInternalServer returns a new gRPC server for Dapr to Dapr communications.
func NewInternalServer(api API, config ServerConfig, tracingSpec config.TracingSpec, metricSpec config.MetricSpec, authenticator auth.Authenticator, proxy messaging.Proxy) Server {
	return &server{
		api:              api,
		config:           config,
		tracingSpec:      tracingSpec,
		metricSpec:       metricSpec,
		authenticator:    authenticator,
		kind:             internalServer,
		logger:           internalServerLogger,
		maxConnectionAge: getDefaultMaxAgeDuration(),
		proxy:            proxy,
	}
}

func getDefaultMaxAgeDuration() *time.Duration {
	d := time.Second * defaultMaxConnectionAgeSeconds
	return &d
}

// StartNonBlocking starts a new server in a goroutine.
func (s *server) StartNonBlocking() error {
	var listeners []net.Listener
	if s.config.UnixDomainSocket != "" && s.kind == apiServer {
		socket := fmt.Sprintf("%s/dapr-%s-grpc.socket", s.config.UnixDomainSocket, s.config.AppID)
		l, err := net.Listen("unix", socket)
		if err != nil {
			return err
		}
		s.logger.Infof("gRPC server listening on UNIX socket: %s", socket)
		listeners = append(listeners, l)
	} else {
		for _, apiListenAddress := range s.config.APIListenAddresses {
			addr := apiListenAddress + ":" + strconv.Itoa(s.config.Port)
			l, err := net.Listen("tcp", addr)
			if err != nil {
				s.logger.Debugf("Failed to listen for gRPC server on TCP address %s with error: %v", addr, err)
			} else {
				s.logger.Infof("gRPC server listening on TCP address: %s", addr)
				listeners = append(listeners, l)
			}
		}
	}

	if len(listeners) == 0 {
		return errors.New("could not listen on any endpoint")
	}

	for _, listener := range listeners {
		// server is created in a loop because each instance
		// has a handle on the underlying listener.
		server, err := s.getGRPCServer()
		if err != nil {
			return err
		}
		s.servers = append(s.servers, server)

		if s.kind == internalServer {
			internalv1pb.RegisterServiceInvocationServer(server, s.api)
		} else if s.kind == apiServer {
			runtimev1pb.RegisterDaprServer(server, s.api)
			if s.workflowEngine != nil {
				s.logger.Infof("Registering workflow engine for gRPC endpoint: %s", listener.Addr())
				s.workflowEngine.RegisterGrpcServer(server)
			}
		}

		go func(server *grpcGo.Server, l net.Listener) {
			if err := server.Serve(l); err != nil {
				s.logger.Fatalf("gRPC serve error: %v", err)
			}
		}(server, listener)
	}
	return nil
}

func (s *server) Close() error {
	for _, server := range s.servers {
		// This calls `Close()` on the underlying listener.
		server.GracefulStop()
	}

	if s.api != nil {
		if closer, ok := s.api.(io.Closer); ok {
			closer.Close()
		}
	}

	return nil
}

func (s *server) generateWorkloadCert() error {
	s.logger.Info("sending workload csr request to sentry")
	signedCert, err := s.authenticator.CreateSignedWorkloadCert(s.config.AppID, s.config.NameSpace, s.config.TrustDomain)
	if err != nil {
		return fmt.Errorf("error from authenticator CreateSignedWorkloadCert: %w", err)
	}
	s.logger.Info("certificate signed successfully")

	tlsCert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
	if err != nil {
		return fmt.Errorf("error creating x509 Key Pair: %w", err)
	}

	s.signedCert = signedCert
	s.tlsCert = tlsCert
	s.signedCertDuration = signedCert.Expiry.Sub(time.Now().UTC())
	return nil
}

func (s *server) getMiddlewareOptions() []grpcGo.ServerOption {
	intr := make([]grpcGo.UnaryServerInterceptor, 0, 6)
	intrStream := make([]grpcGo.StreamServerInterceptor, 0, 5)

	intr = append(intr, metadata.SetMetadataInContextUnary)

	if len(s.apiSpec.Allowed) > 0 || len(s.apiSpec.Denied) > 0 {
		s.logger.Info("Enabled API access list on gRPC server")
		unary, stream := setAPIEndpointsMiddlewares(s.apiSpec.Allowed, s.apiSpec.Denied)
		if unary != nil && stream != nil {
			intr = append(intr, unary)
			intrStream = append(intrStream, stream)
		}
	}

	if s.authToken != "" {
		s.logger.Info("Enabled token authentication on gRPC server")
		unary, stream := getAPIAuthenticationMiddlewares(s.authToken, authConsts.APITokenHeader)
		intr = append(intr, unary)
		intrStream = append(intrStream, stream)
	}

	if diagUtils.IsTracingEnabled(s.tracingSpec.SamplingRate) {
		s.logger.Info("Enabled gRPC tracing middleware")
		intr = append(intr, diag.GRPCTraceUnaryServerInterceptor(s.config.AppID, s.tracingSpec))
		intrStream = append(intrStream, diag.GRPCTraceStreamServerInterceptor(s.config.AppID, s.tracingSpec))
	}

	if s.metricSpec.GetEnabled() {
		s.logger.Info("Enabled gRPC metrics middleware")
		intr = append(intr, diag.DefaultGRPCMonitoring.UnaryServerInterceptor())

		if s.kind == apiServer {
			intrStream = append(intrStream, diag.DefaultGRPCMonitoring.StreamingServerInterceptor())
		} else if s.kind == internalServer {
			intrStream = append(intrStream, diag.DefaultGRPCMonitoring.StreamingClientInterceptor())
		}
	}

	if s.config.EnableAPILogging && s.infoLogger != nil {
		unary, stream := s.getGRPCAPILoggingMiddlewares()
		intr = append(intr, unary)
		intrStream = append(intrStream, stream)
	}

	return []grpcGo.ServerOption{
		grpcGo.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(intr...)),
		grpcGo.StreamInterceptor(grpcMiddleware.ChainStreamServer(intrStream...)),
		grpcGo.InTapHandle(metadata.SetMetadataInTapHandle),
	}
}

func (s *server) getGRPCServer() (*grpcGo.Server, error) {
	opts := s.getMiddlewareOptions()
	if s.maxConnectionAge != nil {
		opts = append(opts, grpcGo.KeepaliveParams(keepalive.ServerParameters{MaxConnectionAge: *s.maxConnectionAge}))
	}

	if s.authenticator != nil {
		err := s.generateWorkloadCert()
		if err != nil {
			return nil, err
		}

		//nolint:gosec
		tlsConfig := tls.Config{
			ClientCAs:  s.signedCert.TrustChain,
			ClientAuth: tls.RequireAndVerifyClientCert,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return &s.tlsCert, nil
			},
		}

		// In the internal server, enforce minimum version TLS 1.2
		if s.kind == internalServer {
			tlsConfig.MinVersion = tls.VersionTLS12
		}

		ta := credentials.NewTLS(&tlsConfig)

		opts = append(opts, grpcGo.Creds(ta))
		go s.startWorkloadCertRotation()
	}

	opts = append(opts,
		grpcGo.MaxRecvMsgSize(s.config.MaxRequestBodySizeMB<<20),
		grpcGo.MaxSendMsgSize(s.config.MaxRequestBodySizeMB<<20),
		grpcGo.MaxHeaderListSize(uint32(s.config.ReadBufferSizeKB<<10)),
	)

	if s.proxy != nil {
		opts = append(opts, grpcGo.UnknownServiceHandler(s.proxy.Handler()))
	}

	return grpcGo.NewServer(opts...), nil
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
				s.renewMutex.Unlock()
				continue
			}
			diag.DefaultMonitoring.MTLSWorkLoadCertRotationCompleted()
		}
		s.renewMutex.Unlock()
	}
}

func shouldRenewCert(certExpiryDate time.Time, certDuration time.Duration) bool {
	expiresIn := certExpiryDate.Sub(time.Now())
	expiresInSeconds := expiresIn.Seconds()
	certDurationSeconds := certDuration.Seconds()

	percentagePassed := 100 - ((expiresInSeconds / certDurationSeconds) * 100)
	return percentagePassed >= renewWhenPercentagePassed
}

func (s *server) getGRPCAPILoggingMiddlewares() (grpcGo.UnaryServerInterceptor, grpcGo.StreamServerInterceptor) {
	if s.infoLogger == nil {
		return nil, nil
	}
	return func(ctx context.Context, req any, info *grpcGo.UnaryServerInfo, handler grpcGo.UnaryHandler) (any, error) {
			if info != nil {
				s.printAPILog(ctx, info.FullMethod)
			}
			return handler(ctx, req)
		},
		func(srv any, stream grpcGo.ServerStream, info *grpcGo.StreamServerInfo, handler grpcGo.StreamHandler) error {
			if info != nil {
				s.printAPILog(stream.Context(), info.FullMethod)
			}
			return handler(srv, stream)
		}
}

func (s *server) printAPILog(ctx context.Context, method string) {
	fields := make(map[string]any, 2)
	fields["method"] = method
	if meta, ok := metadata.FromIncomingContext(ctx); ok {
		if val, ok := meta["user-agent"]; ok && len(val) > 0 {
			fields["useragent"] = val[0]
		}
	}
	s.infoLogger.WithFields(fields).Info("gRPC API Called")
}
