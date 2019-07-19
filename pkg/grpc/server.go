package grpc

import (
	"fmt"
	"net"

	log "github.com/Sirupsen/logrus"
	grpc_go "google.golang.org/grpc"

	pb "github.com/actionscore/actions/pkg/proto"
)

type Server interface {
	StartNonBlocking() error
}

type server struct {
	api    API
	config ServerConfig
}

func NewServer(api API, config ServerConfig) Server {
	return &server{
		api:    api,
		config: config,
	}
}

func (s *server) StartNonBlocking() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%v", s.config.Port))
	if err != nil {
		return err
	}

	server := grpc_go.NewServer()
	pb.RegisterActionsServer(server, s.api)

	go func() {
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()

	return nil
}
