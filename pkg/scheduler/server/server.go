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
	"sync"
	"sync/atomic"

	etcdcron "github.com/diagridio/go-etcd-cron"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/authz"
	"github.com/dapr/dapr/pkg/scheduler/server/internal"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type Options struct {
	Security                security.Handler
	ListenAddress           string
	Port                    int
	Mode                    modes.DaprMode
	ReplicaCount            uint32
	ReplicaID               uint32
	DataDir                 string
	EtcdID                  string
	EtcdInitialPeers        []string
	EtcdClientPorts         []string
	EtcdClientHTTPPorts     []string
	EtcdSpaceQuota          int64
	EtcdCompactionMode      string
	EtcdCompactionRetention string
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	listenAddress string
	port          int
	replicaCount  uint32
	replicaID     uint32

	sec            security.Handler
	authz          *authz.Authz
	config         *embed.Config
	cron           etcdcron.Interface
	connectionPool *internal.Pool // Connection pool for sidecars

	wg      sync.WaitGroup
	running atomic.Bool
	readyCh chan struct{}

	closeCh chan struct{}
}

func New(opts Options) (*Server, error) {
	config, err := config(opts)
	if err != nil {
		return nil, err
	}

	return &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		replicaCount:  opts.ReplicaCount,
		replicaID:     opts.ReplicaID,
		sec:           opts.Security,
		authz: authz.New(authz.Options{
			Security: opts.Security,
		}),
		config:         config,
		connectionPool: internal.NewPool(),
		readyCh:        make(chan struct{}),
		closeCh:        make(chan struct{}),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("server is already running")
	}

	log.Info("Dapr Scheduler is starting...")

	defer s.wg.Wait()
	return concurrency.NewRunnerManager(
		s.connectionPool.Run,
		s.runServer,
		s.runEtcdCron,
		func(ctx context.Context) error {
			<-ctx.Done()
			close(s.closeCh)
			return nil
		},
	).Run(ctx)
}

func (s *Server) runServer(ctx context.Context) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.listenAddress, s.port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.port, err)
	}

	log.Infof("Dapr Scheduler listening on: %s:%d", s.listenAddress, s.port)

	srv := grpc.NewServer(s.sec.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(srv, s)

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

func (s *Server) runEtcdCron(ctx context.Context) error {
	log.Info("Starting etcd")

	etcd, err := embed.StartEtcd(s.config)
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

	endpoints := make([]string, 0, len(etcd.Clients))
	for _, peer := range etcd.Clients {
		endpoints = append(endpoints, peer.Addr().String())
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
	})
	if err != nil {
		return err
	}

	// pass in initial cluster endpoints, but with client ports
	s.cron, err = etcdcron.New(etcdcron.Options{
		Client:         client,
		Namespace:      "dapr",
		PartitionID:    s.replicaID,
		PartitionTotal: s.replicaCount,
		TriggerFn:      s.triggerJob,
	})
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}
	close(s.readyCh)

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			if err := s.cron.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return nil
		},
		func(ctx context.Context) error {
			defer log.Info("EtcdCron shutting down")
			select {
			case err := <-etcd.Err():
				return err
			case <-ctx.Done():
				return nil
			}
		},
	).Run(ctx)
}
