/*
Copyright 2021 The Dapr Authors
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

package placement

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/kit/logger"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

var log = logger.NewLogger("dapr.placement")

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer //nolint:nosnakecase

const (
	// membershipChangeChSize is the channel size of membership change request from Dapr runtime.
	// MembershipChangeWorker will process actor host member change request.
	membershipChangeChSize = 100

	// faultyHostDetectDuration is the maximum duration when existing host is marked as faulty.
	// Dapr runtime sends heartbeat every 1 second. Whenever placement server gets the heartbeat,
	// it updates the last heartbeat time in UpdateAt of the FSM state. If Now - UpdatedAt exceeds
	// faultyHostDetectDuration, membershipChangeWorker() tries to remove faulty Dapr runtim/ from
	// membership.
	// When placement gets the leadership, faultyHostDetectionDuration will be faultyHostDetectInitialDuration.
	// This duration will give more time to let each runtime find the leader of placement nodes.
	// Once the first dissemination happens after getting leadership, membershipChangeWorker will
	// use faultyHostDetectDefaultDuration.
	faultyHostDetectInitialDuration = 6 * time.Second
	faultyHostDetectDefaultDuration = 3 * time.Second

	// faultyHostDetectInterval is the interval to check the faulty member.
	faultyHostDetectInterval = 500 * time.Millisecond

	// disseminateTimerInterval is the interval to disseminate the latest consistent hashing table.
	disseminateTimerInterval = 500 * time.Millisecond
	// disseminateTimeout is the timeout to disseminate hashing tables after the membership change.
	// When the multiple actor service pods are deployed first, a few pods are deployed in the beginning
	// and the rest of pods will be deployed gradually. disseminateNextTime is maintained to decide when
	// the hashing table is disseminated. disseminateNextTime is updated whenever membership change
	// is applied to raft state or each pod is deployed. If we increase disseminateTimeout, it will
	// reduce the frequency of dissemination, but it will delay the table dissemination.
	disseminateTimeout = 2 * time.Second
)

type hostMemberChange struct {
	cmdType raft.CommandType
	host    raft.DaprHostMember
}

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	// serverListener is the TCP listener for placement gRPC server.
	serverListener net.Listener

	// grpcServer is the gRPC server for placement service.
	grpcServer *grpc.Server

	// streamConnPool has the stream connections established between placement gRPC server and Dapr runtime.
	streamConnPool []placementGRPCStream

	// streamConnPoolLock is the lock for streamConnPool change.
	streamConnPoolLock sync.RWMutex

	// raftNode is the raft server instance.
	raftNode *raft.Server

	// lastHeartBeat represents the last time stamp when runtime sent heartbeat.
	lastHeartBeat sync.Map
	// membershipCh is the channel to maintain Dapr runtime host membership update.
	membershipCh chan hostMemberChange
	// disseminateLock is the lock for hashing table dissemination.
	disseminateLock sync.Mutex
	// disseminateNextTime is the time when the hashing tables are disseminated.
	disseminateNextTime atomic.Int64
	// memberUpdateCount represents how many dapr runtimes needs to change.
	// consistent hashing table. Only actor runtime's heartbeat will increase this.
	memberUpdateCount atomic.Uint32

	// faultyHostDetectDuration
	faultyHostDetectDuration *atomic.Int64

	// hasLeadership indicates the state for leadership.
	hasLeadership atomic.Bool

	// streamConnGroup represents the number of stream connections.
	// This waits until all stream connections are drained when revoking leadership.
	streamConnGroup sync.WaitGroup
}

// NewPlacementService returns a new placement service.
func NewPlacementService(raftNode *raft.Server) *Service {
	fhdd := &atomic.Int64{}
	fhdd.Store(int64(faultyHostDetectInitialDuration))

	return &Service{
		streamConnPool:           []placementGRPCStream{},
		membershipCh:             make(chan hostMemberChange, membershipChangeChSize),
		faultyHostDetectDuration: fhdd,
		raftNode:                 raftNode,
	}
}

// Run starts the placement service gRPC server.
func (p *Service) Run(ctx context.Context, port string, certChain *daprCredentials.CertChain) error {
	var err error
	p.serverListener, err = net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	opts, err := daprCredentials.GetServerOptions(certChain)
	if err != nil {
		return fmt.Errorf("error creating gRPC options: %w", err)
	}
	p.grpcServer = grpc.NewServer(opts...)
	placementv1pb.RegisterPlacementServer(p.grpcServer, p)

	log.Infof("starting placement service started on port %d", p.serverListener.Addr().(*net.TCPAddr).Port)

	stopErr := make(chan error, 1)
	go func() {
		if err = p.grpcServer.Serve(p.serverListener); err != nil &&
			!errors.Is(err, grpc.ErrServerStopped) {
			stopErr <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		stopErr <- nil
	}()

	select {
	case <-ctx.Done():
		p.grpcServer.GracefulStop()
		err = <-stopErr
	case err = <-stopErr:
		// nop
	}
	return err
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(stream placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	registeredMemberID := ""
	isActorRuntime := false

	p.streamConnGroup.Add(1)
	defer func() {
		p.streamConnGroup.Done()
		p.deleteStreamConn(stream)
	}()

	for p.hasLeadership.Load() {
		req, err := stream.Recv()
		switch err {
		case nil:
			if registeredMemberID == "" {
				registeredMemberID = req.Name
				p.addStreamConn(stream)
				// TODO: If each sidecar can report table version, then placement
				// doesn't need to disseminate tables to each sidecar.
				err = p.performTablesUpdate(stream.Context(), []placementGRPCStream{stream}, p.raftNode.FSM().PlacementState())
				if err != nil {
					return err
				}
				log.Debugf("Stream connection is established from %s", registeredMemberID)
			}

			// Ensure that the incoming runtime is actor instance.
			isActorRuntime = len(req.Entities) > 0
			if !isActorRuntime {
				// ignore if this runtime is non-actor.
				continue
			}

			// Record the heartbeat timestamp. This timestamp will be used to check if the member
			// state maintained by raft is valid or not. If the member is outdated based the timestamp
			// the member will be marked as faulty node and removed.
			p.lastHeartBeat.Store(req.Name, time.Now().UnixNano())

			members := p.raftNode.FSM().State().Members()

			// Upsert incoming member only if it is an actor service (not actor client) and
			// the existing member info is unmatched with the incoming member info.
			upsertRequired := true
			if m, ok := members[req.Name]; ok {
				if m.AppID == req.Id && m.Name == req.Name && cmp.Equal(m.Entities, req.Entities) {
					upsertRequired = false
				}
			}

			if upsertRequired {
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberUpsert,
					host: raft.DaprHostMember{
						Name:      req.Name,
						AppID:     req.Id,
						Entities:  req.Entities,
						UpdatedAt: time.Now().UnixNano(),
					},
				}
			}

		default:
			if registeredMemberID == "" {
				log.Error("Stream is disconnected before member is added")
				return nil
			}

			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				log.Debugf("Stream connection is disconnected gracefully: %s", registeredMemberID)
				if isActorRuntime {
					p.membershipCh <- hostMemberChange{
						cmdType: raft.MemberRemove,
						host:    raft.DaprHostMember{Name: registeredMemberID},
					}
				}
			} else {
				// no actions for hashing table. Instead, MembershipChangeWorker will check
				// host updatedAt and if now - updatedAt > p.faultyHostDetectDuration, remove hosts.
				log.Debugf("Stream connection is disconnected with the error: %v", err)
			}

			return nil
		}
	}

	return status.Error(codes.FailedPrecondition, "only leader can serve the request")
}

// addStreamConn adds stream connection between runtime and placement to the dissemination pool.
func (p *Service) addStreamConn(conn placementGRPCStream) {
	p.streamConnPoolLock.Lock()
	p.streamConnPool = append(p.streamConnPool, conn)
	p.streamConnPoolLock.Unlock()
}

func (p *Service) deleteStreamConn(conn placementGRPCStream) {
	p.streamConnPoolLock.Lock()
	for i, c := range p.streamConnPool {
		if c == conn {
			p.streamConnPool = append(p.streamConnPool[:i], p.streamConnPool[i+1:]...)
			break
		}
	}
	p.streamConnPoolLock.Unlock()
}

func (p *Service) hasStreamConn(conn placementGRPCStream) bool {
	p.streamConnPoolLock.RLock()
	defer p.streamConnPoolLock.RUnlock()

	for _, c := range p.streamConnPool {
		if c == conn {
			return true
		}
	}
	return false
}
