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
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcGo "google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	grpcInsecure "google.golang.org/grpc/credentials/insecure"
	grpcKeepalive "google.golang.org/grpc/keepalive"
	grpcReflection "google.golang.org/grpc/reflection"
	grpcStatus "google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messaging"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

const (
	certWatchInterval         = time.Second * 3
	renewWhenPercentagePassed = 70
	apiServer                 = "apiServer"
	internalServer            = "internalServer"
)

// Server is an interface for the dapr gRPC server.
type Server interface {
	io.Closer
	StartNonBlocking() error
}

type server struct {
	api            API
	config         ServerConfig
	tracingSpec    config.TracingSpec
	metricSpec     config.MetricSpec
	servers        []*grpcGo.Server
	kind           string
	logger         logger.Logger
	infoLogger     logger.Logger
	grpcServerOpts []grpcGo.ServerOption
	authToken      string
	apiSpec        config.APISpec
	proxy          messaging.Proxy
	workflowEngine *wfengine.WorkflowEngine
	sec            security.Handler
	wg             sync.WaitGroup
	closed         atomic.Bool
	closeCh        chan struct{}
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
		authToken:      security.GetAPIToken(),
		apiSpec:        apiSpec,
		proxy:          proxy,
		workflowEngine: workflowEngine,
		closeCh:        make(chan struct{}),
	}
}

// NewInternalServer returns a new gRPC server for Dapr to Dapr communications.
func NewInternalServer(api API, config ServerConfig, tracingSpec config.TracingSpec, metricSpec config.MetricSpec, sec security.Handler, proxy messaging.Proxy) Server {
	// This is equivalent to "infinity" time (see: https://github.com/grpc/grpc-go/blob/master/internal/transport/defaults.go)
	const infinity = time.Duration(math.MaxInt64)

	serverOpts := []grpcGo.ServerOption{
		grpcGo.KeepaliveEnforcementPolicy(grpcKeepalive.EnforcementPolicy{
			// If a client pings more than once every 8s, terminate the connection
			MinTime: 8 * time.Second,
			// Allow pings even when there are no active streams
			PermitWithoutStream: true,
		}),
		grpcGo.KeepaliveParams(grpcKeepalive.ServerParameters{
			// Do not set a max age
			// The client uses a pool that recycles connections automatically
			MaxConnectionAge: infinity,
			// Do not forcefully close connections if there are pending RPCs
			MaxConnectionAgeGrace: infinity,
			// If a client is idle for 3m, send a GOAWAY
			// This is equivalent to the max idle time set in the client
			MaxConnectionIdle: 3 * time.Minute,
			// Ping the client if it is idle for 10s to ensure the connection is still active
			Time: 10 * time.Second,
			// Wait 5s for the ping ack before assuming the connection is dead
			Timeout: 5 * time.Second,
		}),
	}
	return &server{
		api:            api,
		config:         config,
		tracingSpec:    tracingSpec,
		metricSpec:     metricSpec,
		kind:           internalServer,
		logger:         internalServerLogger,
		grpcServerOpts: serverOpts,
		proxy:          proxy,
		sec:            sec,
		closeCh:        make(chan struct{}),
	}
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
				s.logger.Errorf("Failed to listen for gRPC server on TCP address %s with error: %v", addr, err)
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
		grpcReflection.Register(server)
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

		s.wg.Add(1)
		go func(server *grpcGo.Server, l net.Listener) {
			defer s.wg.Done()
			if err := server.Serve(l); err != nil && !errors.Is(err, grpcGo.ErrServerStopped) {
				s.logger.Fatalf("gRPC serve error: %v", err)
			}
		}(server, listener)
	}
	return nil
}

func (s *server) Close() error {
	defer s.wg.Wait()
	if s.closed.CompareAndSwap(false, true) {
		close(s.closeCh)
	}

	s.wg.Add(len(s.servers))
	for _, server := range s.servers {
		// This calls `Close()` on the underlying listener.
		go func(server *grpcGo.Server) {
			defer s.wg.Done()
			server.GracefulStop()
		}(server)
	}

	if s.api != nil {
		if closer, ok := s.api.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *server) getMiddlewareOptions() []grpcGo.ServerOption {
	// We initialize these slices with an initial capacity to give the compiler a "hint" of how much memory we may use.
	// These capacities are the worst-case scenario below (max number of items added to each slice).
	// Specifying an initial capacity helps us reducing the risk that we may need to re-allocate the slice, which is wasteful both on the allocator and on the GC.
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
		unary, stream := getAPIAuthenticationMiddlewares(s.authToken, securityConsts.APITokenHeader)
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
	if len(s.grpcServerOpts) > 0 {
		opts = append(opts, s.grpcServerOpts...)
	}

	opts = append(opts,
		grpcGo.MaxRecvMsgSize(s.config.MaxRequestBodySizeMB<<20),
		grpcGo.MaxSendMsgSize(s.config.MaxRequestBodySizeMB<<20),
		grpcGo.MaxHeaderListSize(uint32(s.config.ReadBufferSizeKB<<10)),
	)

	if s.sec == nil {
		opts = append(opts, grpcGo.Creds(grpcInsecure.NewCredentials()))
	} else {
		opts = append(opts, s.sec.GRPCServerOptionMTLS())
	}

	if s.proxy != nil {
		opts = append(opts, grpcGo.UnknownServiceHandler(s.proxy.Handler()))
	}

	return grpcGo.NewServer(opts...), nil
}

func (s *server) getGRPCAPILoggingMiddlewares() (grpcGo.UnaryServerInterceptor, grpcGo.StreamServerInterceptor) {
	if s.infoLogger == nil {
		return nil, nil
	}

	return func(ctx context.Context, req any, info *grpcGo.UnaryServerInfo, handler grpcGo.UnaryHandler) (any, error) {
			// Invoke the handler
			start := time.Now()
			res, err := handler(ctx, req)

			// Print the API logs
			if info != nil {
				s.printAPILog(ctx, info.FullMethod, time.Since(start), grpcStatus.Code(err))
			}

			// Return the response
			return res, err
		},
		func(srv any, stream grpcGo.ServerStream, info *grpcGo.StreamServerInfo, handler grpcGo.StreamHandler) error {
			// Invoke the handler
			start := time.Now()
			err := handler(srv, stream)

			// Print the API logs
			if info != nil {
				s.printAPILog(stream.Context(), info.FullMethod, time.Since(start), grpcStatus.Code(err))
			}

			// Return the response
			return err
		}
}

func (s *server) printAPILog(ctx context.Context, method string, duration time.Duration, code grpcCodes.Code) {
	fields := make(map[string]any, 4)
	fields["method"] = method
	if meta, ok := metadata.FromIncomingContext(ctx); ok {
		if val, ok := meta["user-agent"]; ok && len(val) > 0 {
			fields["useragent"] = val[0]
		}
	}
	// Report duration in milliseconds
	fields["duration"] = duration.Milliseconds()
	fields["code"] = int32(code)
	s.infoLogger.WithFields(fields).Info("gRPC API Called")
}
