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
	"errors"
	"fmt"
	"net"

	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	etcdcron "github.com/Scalingo/go-etcd-cron"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type Options struct {
	// Port is the port that the server will listen on.
	Port     int
	Security security.Handler
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	port int
	srv  *grpc.Server

	cron    *etcdcron.Cron
	readyCh chan struct{}
}

func New(opts Options) *Server {
	s := &Server{
		port:    opts.Port,
		readyCh: make(chan struct{}),
	}

	s.srv = grpc.NewServer(opts.Security.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	return s
}

func (s *Server) Run(ctx context.Context) error {
	log.Info("Dapr Scheduler is starting...")
	return concurrency.NewRunnerManager(
		s.runServer,
		s.runETCD,
	).Run(ctx)
}

func (s *Server) runServer(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.port, err)
	}

	errCh := make(chan error)
	go func() {
		log.Infof("Running gRPC server on port %d", s.port)
		if err := s.srv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
	}()

	select {
	case err = <-errCh:
		return err
	case <-ctx.Done():
		log.Info("Shutting down gRPC server")

		s.srv.GracefulStop()

		return nil
	}
}

func (s *Server) runETCD(ctx context.Context) error {
	log.Info("Starting etcd concurrently...")

	etcd, err := embed.StartEtcd(conf())
	if err != nil {
		return err
	}
	defer etcd.Close()

	select {
	case <-etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		etcd.Server.Stop()
		return errors.New("etcd server took too long to start")
	}

	log.Info("Starting ETCDCron")
	cron, err := etcdcron.New()
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	cron.Start(ctx)
	defer cron.Stop()

	s.cron = cron
	close(s.readyCh)

	select {
	case err := <-etcd.Err():
		return err
	case <-ctx.Done():
		return nil
	}
}
