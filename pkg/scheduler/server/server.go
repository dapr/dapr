/*
Copyright 2023 The Dapr Authors
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

package server

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type SchedulerServiceOpts struct {
	// Port is the port that the server will listen on.
	Port int

	Security security.Handler
}

// server is the gRPC server for the Scheduler service.
type server struct {
	opts       SchedulerServiceOpts
	srv        *grpc.Server
	shutdownCh chan struct{}

	connectedHosts map[string][]string
}

// Start starts the server. Blocks until the context is cancelled.
func Start(ctx context.Context, opts SchedulerServiceOpts) error {
	// Init the server
	s := &server{}
	err := s.Init(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to init server: %w", err)
	}

	return s.Run(ctx)
}

func (s *server) Init(ctx context.Context, opts SchedulerServiceOpts) (err error) {
	s.opts = opts
	s.shutdownCh = make(chan struct{})
	s.connectedHosts = make(map[string][]string)

	// TODO - init etcd somewhere here

	// Create the gRPC server
	s.srv = grpc.NewServer(opts.Security.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	return nil
}

func (s *server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.opts.Port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.opts.Port, err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Infof("Running gRPC server on port %d", s.opts.Port)
		if err := s.srv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		errCh <- nil
	}()

	<-ctx.Done()
	log.Info("Shutting down gRPC server")

	gracefulShutdownCh := make(chan struct{})
	go func() {
		s.srv.GracefulStop()
		close(gracefulShutdownCh)
	}()
	close(s.shutdownCh)
	<-gracefulShutdownCh

	return <-errCh
}

func (s *server) ConnectHost(context.Context, *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ScheduleJob is a placeholder method that needs to be implemented
func (s *server) ScheduleJob(context.Context, *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ListJobs is a placeholder method that needs to be implemented
func (s *server) ListJobs(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetJob is a placeholder method that needs to be implemented
func (s *server) GetJob(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// DeleteJob is a placeholder method that needs to be implemented
func (s *server) DeleteJob(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
