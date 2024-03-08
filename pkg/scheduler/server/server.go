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
	"os"
	"sync/atomic"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	etcdcron "github.com/Scalingo/go-etcd-cron"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/config"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	globalconfig "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type Options struct {
	AppID         string
	HostAddress   string
	ListenAddress string
	DataDir       string
	Mode          modes.DaprMode
	Port          int

	Security security.Handler

	PlacementAddress string
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	port          int
	srv           *grpc.Server
	listenAddress string
	dataDir       string

	cron    *etcdcron.Cron
	readyCh chan struct{}

	grpcManager  *manager.Manager
	actorRuntime actors.ActorRuntime
}

func New(opts Options) *Server {
	s := &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		dataDir:       opts.DataDir,
		readyCh:       make(chan struct{}),
	}

	s.srv = grpc.NewServer(opts.Security.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	apiLevel := &atomic.Uint32{}
	apiLevel.Store(config.ActorAPILevel)

	if opts.PlacementAddress != "" {
		// Create gRPC manager
		grpcAppChannelConfig := &manager.AppChannelConfig{}
		s.grpcManager = manager.NewManager(opts.Security, opts.Mode, grpcAppChannelConfig)
		s.grpcManager.StartCollector()

		act, _ := actors.NewActors(actors.ActorsOpts{
			AppChannel:       nil,
			GRPCConnectionFn: s.grpcManager.GetGRPCConnection,
			Config: actors.Config{
				Config: config.Config{
					ActorsService:                 "placement:" + opts.PlacementAddress,
					AppID:                         opts.AppID,
					HostAddress:                   opts.HostAddress,
					Port:                          s.port,
					PodName:                       os.Getenv("POD_NAME"),
					HostedActorTypes:              config.NewHostedActors([]string{}),
					ActorDeactivationScanInterval: time.Hour, // TODO: disable this feature since we just need to invoke actors
				},
			},
			TracingSpec:     globalconfig.TracingSpec{},
			Resiliency:      resiliency.New(log),
			StateStoreName:  "",
			CompStore:       nil,
			StateTTLEnabled: false, // artursouza: this should not be relevant to invoke actors.
			Security:        opts.Security,
		})

		s.actorRuntime = act
	}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	log.Info("Dapr Scheduler is starting...")

	if s.actorRuntime != nil {
		log.Info("Initializing actor runtime")
		err := s.actorRuntime.Init(ctx)
		if err != nil {
			return err
		}
	}
	return concurrency.NewRunnerManager(
		s.runServer,
		s.runEtcd,
	).Run(ctx)
}

func (s *Server) runServer(ctx context.Context) error {
	var listener net.Listener
	var err error

	if s.listenAddress != "" {
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", s.listenAddress, s.port))
		if err != nil {
			return fmt.Errorf("could not listen on port %d: %w", s.port, err)
		}
		log.Infof("Dapr Scheduler listening on: %s:%d", s.listenAddress, s.port)
	} else {
		listener, err = net.Listen("tcp", fmt.Sprintf(":%d", s.port))
		if err != nil {
			return fmt.Errorf("could not listen on port %d: %w", s.port, err)
		}
		log.Infof("Dapr Scheduler listening on port :%d", s.port)
	}

	errCh := make(chan error)
	go func() {
		log.Infof("Running gRPC server on port %d", s.port)
		if nerr := s.srv.Serve(listener); nerr != nil {
			errCh <- fmt.Errorf("failed to serve: %w", nerr)
			return
		}
	}()

	select {
	case err = <-errCh:
		return err
	case <-ctx.Done():
		s.srv.GracefulStop()
		log.Info("Scheduler GRPC server stopped")
		return nil
	}
}

func (s *Server) runEtcd(ctx context.Context) error {
	log.Info("Starting etcd")

	etcd, err := embed.StartEtcd(s.conf())
	if err != nil {
		return err
	}
	defer etcd.Close()

	select {
	case <-etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		return ctx.Err()
	}

	log.Info("Starting EtcdCron")
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
		log.Info("Embedded Etcd shutting down")
		return nil
	}
}
