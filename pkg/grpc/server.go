// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"fmt"
	"net"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	dapr_pb "github.com/dapr/dapr/pkg/proto/dapr"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	log "github.com/sirupsen/logrus"
	grpc_go "google.golang.org/grpc"
)

// Server is an interface for the dapr gRPC server
type Server interface {
	StartNonBlocking() error
}

type server struct {
	api         API
	config      ServerConfig
	tracingSpec config.TracingSpec
}

// NewServer returns a new gRPC server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%v", s.config.Port))
	if err != nil {
		return err
	}

	server := grpc_go.NewServer()
	if s.tracingSpec.Enabled {
		server = grpc_go.NewServer(
			grpc_go.StreamInterceptor(diag.TracingGRPCMiddleware(s.tracingSpec)),
			grpc_go.UnaryInterceptor(diag.TracingGRPCMiddlewareUnary(s.tracingSpec)),
		)
	}
	daprinternal_pb.RegisterDaprInternalServer(server, s.api)
	dapr_pb.RegisterDaprServer(server, s.api)

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()
	return nil
}
