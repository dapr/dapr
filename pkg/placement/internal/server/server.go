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
	"github.com/dapr/dapr/pkg/placement/internal/standdown"
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

	DisseminateTimeout        time.Duration
	DisseminateCoalesceWindow time.Duration

	// SchedulerAddresses, when set, are watched for a scheduler placement
	// leader advertisement, on which this placement service stands down.
	SchedulerAddresses []string
}

type Server struct {
	nodeID                    string
	port                      int
	listenAddress             string
	leadership                *leadership.Leadership
	sec                       security.Handler
	htarget                   healthz.Target
	keepAliveTime             time.Duration
	keepAliveTimeout          time.Duration
	replicationFactor         int64
	disseminateTimeout        time.Duration
	disseminateCoalesceWindow time.Duration
	schedulerAddresses        []string

	authz     *authorizer.Authorizer
	loop      loop.Interface[loops.EventNamespace]
	standdown *standdown.StandDown

	isLeader     atomic.Bool
	shutdown     atomic.Bool
	standingDown atomic.Bool
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
		replicationFactor:         opts.ReplicationFactor,
		disseminateTimeout:        opts.DisseminateTimeout,
		disseminateCoalesceWindow: opts.DisseminateCoalesceWindow,
		schedulerAddresses:        opts.SchedulerAddresses,
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
		CancelPool:                  cancel,
		ReplicationFactor:           s.replicationFactor,
		Authorizer:                  s.authz,
		DisseminationTimeout:        s.disseminateTimeout,
		DisseminationCoalesceWindow: s.disseminateCoalesceWindow,
	})

	s.htarget.Ready()

	// Stands the placement service down once a scheduler signals the actor
	// placement cutover, so a cluster never has two placement authorities.
	// Streams are drained with a final empty table, then the leader commits
	// the stand-down through raft and confirms it to the schedulers.
	s.standdown = standdown.New(standdown.Options{
		Addresses: s.schedulerAddresses,
		Security:  s.sec,
		OnStandDown: func() {
			s.standingDown.Store(true)
			s.loop.Enqueue(&loops.StandDown{
				Error: standDownErr(),
				Done: func() {
					go s.commitAndConfirmStandDown(ctx)
				},
			})
		},
		OnStandUp: func() {
			s.standingDown.Store(false)
			s.loop.Enqueue(new(loops.StandUp))
			go s.commitServe(ctx)
		},
	})

	return concurrency.NewRunnerManager(
		s.standdown.Run,
		s.loop.Run,
		func(ctx context.Context) error {
			log.Infof("Node id=%s is waiting for leadership", s.nodeID)
			if lerr := s.leadership.Wait(ctx); lerr != nil {
				return lerr
			}

			// A committed stand-down outlives the leader which drained
			// the streams, so inherit it before serving a single stream.
			stood, serr := s.leadership.StoodDown(ctx)
			if serr != nil {
				return serr
			}
			if stood {
				s.standingDown.Store(true)
				s.standdown.Inherit()
				s.loop.Enqueue(&loops.StandDown{
					Error: standDownErr(),
					Done:  func() {},
				})
			}

			log.Infof("Node id=%s has acquired leadership", s.nodeID)
			s.isLeader.Store(true)
			monitoring.RecordPlacementLeaderStatus(true)
			monitoring.RecordRaftPlacementLeaderStatus(true)

			switch {
			case stood:
				// The previous leader may have died before the confirmation
				// was delivered.
				go s.standdown.Confirm(ctx)
			case s.standingDown.Load():
				// The watcher stood this pod down before it had leadership,
				// so the commit and confirmation could not happen then.
				go s.commitAndConfirmStandDown(ctx)
			}

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
			s.shutdown.Store(true)
			s.loop.Close(&loops.Shutdown{
				Error: context.Cause(ctx),
			})
			gserver.GracefulStop()
			log.Info("Placement GRPC server stopped")
			return nil
		},
	).Run(ctx)
}

func (s *Server) StatePlacementTables(ctx context.Context) (*v1pb.StatePlacementTables, error) {
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

// commitAndConfirmStandDown makes the completed stand-down durable and
// reports it to the schedulers. Leader only, since a follower confirming
// before the leader drained would let the schedulers advertise next to a
// still-serving placement leader.
func (s *Server) commitAndConfirmStandDown(ctx context.Context) {
	if !s.isLeader.Load() {
		return
	}

	for {
		err := s.leadership.CommitStandDown(ctx)
		if err == nil {
			break
		}
		log.Errorf("Failed to commit the stand-down to the raft log, retrying: %s", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}

	s.standdown.Confirm(ctx)
}

// commitServe replicates the revoked stand-down so a later leader does not
// inherit it. Leader only.
func (s *Server) commitServe(ctx context.Context) {
	if !s.isLeader.Load() {
		return
	}

	for {
		err := s.leadership.CommitServe(ctx)
		if err == nil {
			return
		}
		log.Errorf("Failed to commit the revoked stand-down to the raft log, retrying: %s", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func standDownErr() error {
	return status.Error(
		codes.FailedPrecondition,
		"actor placement is served by the scheduler control plane and this placement service is standing down. Upgrade this sidecar to a Dapr version supporting scheduler placement",
	)
}

func (s *Server) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	// Serving waits for the watcher's first observation of the scheduler
	// cluster, so a placement service restarting after a cutover cannot
	// serve before one look at the advertisement. An unreachable scheduler
	// cluster completes the observation too, placement fails open.
	select {
	case <-s.standdown.FirstObservation():
	case <-stream.Context().Done():
		return stream.Context().Err()
	}

	if s.standingDown.Load() {
		return standDownErr()
	}

	if !s.isLeader.Load() {
		return status.Errorf(
			codes.FailedPrecondition,
			"node id=%s is not a leader. Only the leader can serve requests",
			s.nodeID,
		)
	}

	if s.shutdown.Load() {
		return status.Errorf(
			codes.Unavailable,
			"placement server is shutting down",
		)
	}

	host, err := stream.Recv()
	if err != nil {
		return err
	}

	if err = s.authz.Host(stream, host); err != nil {
		return err
	}

	log.Infof("Received status report connection from new namespace=%s id=%s host=%s",
		host.GetNamespace(), host.GetId(), host.GetName())

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
