/*
Copyright 2024 The Dapr Authors
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
	"math"
	"net"
	"strconv"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/controller"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/cron"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/etcd"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type Options struct {
	Healthz                   healthz.Healthz
	Security                  security.Handler
	ListenAddress             string
	OverrideBroadcastHostPort *string
	Port                      int
	Mode                      modes.DaprMode
	KubeConfig                *string

	EtcdEmbed                      bool
	EtcdDataDir                    string
	EtcdName                       string
	EtcdInitialCluster             []string
	EtcdClientPort                 uint64
	EtcdSpaceQuota                 int64
	EtcdCompactionMode             string
	EtcdCompactionRetention        string
	EtcdSnapshotCount              uint64
	EtcdMaxSnapshots               uint
	EtcdMaxWALs                    uint
	EtcdBackendBatchLimit          int
	EtcdBackendBatchInterval       string
	EtcdDefragThresholdMB          uint
	EtcdInitialElectionTickAdvance bool
	EtcdMetrics                    string

	EtcdClientEndpoints []string
	EtcdClientUsername  string
	EtcdClientPassword  string
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	listenAddress string
	port          int

	sec        security.Handler
	serializer *serialize.Serializer
	cron       cron.Interface
	etcd       etcd.Interface
	controller concurrency.Runner

	hzAPIServer healthz.Target

	running atomic.Bool

	closeCh chan struct{}
}

func New(opts Options) (*Server, error) {
	var broadcastAddr string
	switch {
	case opts.OverrideBroadcastHostPort != nil:
		broadcastAddr = *opts.OverrideBroadcastHostPort
	case utils.IsLocalhost(opts.ListenAddress):
		broadcastAddr = net.JoinHostPort(opts.ListenAddress, strconv.Itoa(opts.Port))
	default:
		haddr, err := utils.GetHostAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get host address: %w", err)
		}
		broadcastAddr = net.JoinHostPort(haddr, strconv.Itoa(opts.Port))
	}

	etcd, err := etcd.New(etcd.Options{
		Name:                       opts.EtcdName,
		Embed:                      opts.EtcdEmbed,
		InitialCluster:             opts.EtcdInitialCluster,
		ClientPort:                 opts.EtcdClientPort,
		SpaceQuota:                 opts.EtcdSpaceQuota,
		CompactionMode:             opts.EtcdCompactionMode,
		CompactionRetention:        opts.EtcdCompactionRetention,
		SnapshotCount:              opts.EtcdSnapshotCount,
		MaxSnapshots:               opts.EtcdMaxSnapshots,
		MaxWALs:                    opts.EtcdMaxWALs,
		BackendBatchLimit:          opts.EtcdBackendBatchLimit,
		BackendBatchInterval:       opts.EtcdBackendBatchInterval,
		DefragThresholdMB:          opts.EtcdDefragThresholdMB,
		InitialElectionTickAdvance: opts.EtcdInitialElectionTickAdvance,
		Metrics:                    opts.EtcdMetrics,
		Security:                   opts.Security,
		DataDir:                    opts.EtcdDataDir,
		Healthz:                    opts.Healthz,
		Mode:                       opts.Mode,

		ClientEndpoints: opts.EtcdClientEndpoints,
		ClientUsername:  opts.EtcdClientUsername,
		ClientPassword:  opts.EtcdClientPassword,
	})
	if err != nil {
		return nil, err
	}

	cron := cron.New(cron.Options{
		ID:      opts.EtcdName,
		Healthz: opts.Healthz,
		Host:    &schedulerv1pb.Host{Address: broadcastAddr},
		Etcd:    etcd,
	})

	var ctrl concurrency.Runner
	if opts.Mode == modes.KubernetesMode {
		var err error
		ctrl, err = controller.New(controller.Options{
			KubeConfig: opts.KubeConfig,
			Cron:       cron,
			Healthz:    opts.Healthz,
		})
		if err != nil {
			return nil, err
		}
	}

	return &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		sec:           opts.Security,
		controller:    ctrl,
		cron:          cron,
		etcd:          etcd,
		serializer: serialize.New(serialize.Options{
			Security: opts.Security,
		}),
		closeCh:     make(chan struct{}),
		hzAPIServer: opts.Healthz.AddTarget("scheduler-server"),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("server is already running")
	}

	log.Info("Dapr Scheduler is starting...")

	runners := []concurrency.Runner{
		s.etcd.Run,
		s.runServer,
		func(ctx context.Context) error {
			err := s.cron.Run(ctx)
			if ctx.Err() != nil {
				if err != nil {
					log.Errorf("Error running scheduler cron: %s", err)
				}
				return ctx.Err()
			}
			return err
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			close(s.closeCh)
			return nil
		},
	}

	if s.controller != nil {
		runners = append(runners, s.controller)
	}

	mngr := concurrency.NewRunnerCloserManager(log, nil, runners...)
	if err := mngr.AddCloser(s.etcd); err != nil {
		return err
	}

	return mngr.Run(ctx)
}

func (s *Server) runServer(ctx context.Context) error {
	defer s.hzAPIServer.NotReady()
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.listenAddress, s.port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.port, err)
	}

	log.Infof("Dapr Scheduler listening on: %s:%d", s.listenAddress, s.port)

	srv := grpc.NewServer(
		s.sec.GRPCServerOptionMTLS(),
		grpc.MaxSendMsgSize(math.MaxInt32),
		grpc.MaxRecvMsgSize(math.MaxInt32),
	)
	schedulerv1pb.RegisterSchedulerServer(srv, s)

	s.hzAPIServer.Ready()

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			log.Infof("Running gRPC server on port %d", s.port)
			if err := srv.Serve(listener); err != nil {
				return fmt.Errorf("failed to serve: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			srv.GracefulStop()
			log.Info("Scheduler GRPC server stopped")
			return nil
		},
	).Run(ctx)
}
