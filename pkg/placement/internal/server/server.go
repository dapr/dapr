/*
Copyright 2026 The Dapr Authors
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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/internal/authorizer"
	"github.com/dapr/dapr/pkg/placement/internal/leadership"
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/namespaces"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.server")

type Options struct {
	NodeID            string
	Port              int
	ListenAddress     string
	Leadership        *leadership.Leadership
	Security          security.Handler
	Healthz           healthz.Healthz
	KeepAliveTime     time.Duration
	KeepAliveTimeout  time.Duration
	ReplicationFactor int64

	DisseminateTimeout time.Duration
}

type Server struct {
	nodeID             string
	port               int
	listenAddress      string
	leadership         *leadership.Leadership
	sec                security.Handler
	htarget            healthz.Target
	keepAliveTime      time.Duration
	keepAliveTimeout   time.Duration
	replicationFactor  int64
	disseminateTimeout time.Duration

	authz *authorizer.Authorizer
	loop  loop.Interface[loops.Event]

	isLeader atomic.Bool
	readyCh  chan struct{}
}

func New(opts Options) *Server {
	return &Server{
		nodeID:           opts.NodeID,
		port:             opts.Port,
		listenAddress:    opts.ListenAddress,
		leadership:       opts.Leadership,
		sec:              opts.Security,
		htarget:          opts.Healthz.AddTarget("placement-grpc-server"),
		keepAliveTime:    opts.KeepAliveTime,
		keepAliveTimeout: opts.KeepAliveTimeout,
		authz: authorizer.New(authorizer.Options{
			Security: opts.Security,
		}),
		replicationFactor:  opts.ReplicationFactor,
		disseminateTimeout: opts.DisseminateTimeout,
		readyCh:            make(chan struct{}),
	}
}

func (s *Server) Run(ctx context.Context) error {
	defer s.htarget.NotReady()

	log.Info("Placement service is starting...")

	monitoring.RecordPlacementLeaderStatus(false)
	monitoring.RecordRaftPlacementLeaderStatus(false)

	listener, err := net.Listen("tcp",
		net.JoinHostPort(s.listenAddress, strconv.Itoa(s.port)),
	)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	keepaliveParams := keepalive.ServerParameters{
		Time:    s.keepAliveTime,
		Timeout: s.keepAliveTimeout,
	}

	gserver := grpc.NewServer(
		s.sec.GRPCServerOptionMTLS(),
		grpc.KeepaliveParams(keepaliveParams),
	)

	v1pb.RegisterPlacementServer(gserver, s)

	log.Infof("Placement service started on port %d", listener.Addr().(*net.TCPAddr).Port)

	ctx, cancel := context.WithCancelCause(ctx)
	s.loop = namespaces.New(namespaces.Options{
		CancelPool:           cancel,
		ReplicationFactor:    s.replicationFactor,
		Authorizer:           s.authz,
		DisseminationTimeout: s.disseminateTimeout,
	})

	close(s.readyCh)
	s.htarget.Ready()

	return concurrency.NewRunnerManager(
		s.loop.Run,
		func(ctx context.Context) error {
			log.Infof("Node id=%s is waiting for leadership", s.nodeID)
			if lerr := s.leadership.Wait(ctx); lerr != nil {
				return lerr
			}
			log.Infof("Node id=%s has acquired leadership", s.nodeID)
			monitoring.RecordPlacementLeaderStatus(true)
			monitoring.RecordRaftPlacementLeaderStatus(true)
			s.isLeader.Store(true)
			<-ctx.Done()
			return ctx.Err()
		},
		func(ctx context.Context) error {
			log.Infof("Running Placement gRPC server on %s", listener.Addr())
			if err := gserver.Serve(listener); err != nil {
				return fmt.Errorf("failed to serve: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			s.loop.Close(new(loops.Shutdown))
			gserver.GracefulStop()
			log.Info("Placement GRPC server stopped")
			return nil
		},
	).Run(ctx)
}

func (s *Server) StatePlacementTables(ctx context.Context) (*v1pb.StatePlacementTables, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	if !s.isLeader.Load() {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"node id=%s is not a leader. Only the leader can serve requests",
			s.nodeID,
		)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var got *v1pb.StatePlacementTables
	var lock sync.Mutex

	s.loop.Enqueue(&loops.StateTableRequest{
		State: func(result *v1pb.StatePlacementTables) {
			lock.Lock()
			got = result
			lock.Unlock()
			cancel()
		},
	})

	<-ctx.Done()

	lock.Lock()
	defer lock.Unlock()
	return proto.Clone(got).(*v1pb.StatePlacementTables), nil
}

func (s *Server) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	if !s.isLeader.Load() {
		return status.Errorf(
			codes.FailedPrecondition,
			"node id=%s is not a leader. Only the leader can serve requests",
			s.nodeID,
		)
	}

	host, err := stream.Recv()
	if err != nil {
		return err
	}

	if err = s.authz.Host(stream, host); err != nil {
		return err
	}

	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(nil)

	s.loop.Enqueue(&loops.ConnAdd{
		InitialHost: host,
		Channel:     stream,
		Cancel:      cancel,
	})

	<-ctx.Done()

	return context.Cause(ctx)
}
