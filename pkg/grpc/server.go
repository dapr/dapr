package grpc

import (
	"fmt"
	"net"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/config"
	diag "github.com/actionscore/actions/pkg/diagnostics"
	pb "github.com/actionscore/actions/pkg/proto"
	grpc_go "google.golang.org/grpc"
)

// Server is an interface for the actions gRPC server
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
		diag.CreateExporter(s.config.ActionID, s.config.HostAddress, s.tracingSpec, nil)
		server = grpc_go.NewServer(
			grpc_go.StreamInterceptor(diag.TracingGRPCMiddleware(s.tracingSpec)),
			grpc_go.UnaryInterceptor(diag.TracingGRPCMiddlewareUnary(s.tracingSpec)),
		)
	}
	pb.RegisterActionsServer(server, s.api)

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()

	return nil
}
