// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/kit/logger"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

var log = logger.NewLogger("dapr.placement")

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer

const (
	// membershipChangeChSize is the channel size of membership change request from Dapr runtime.
	// MembershipChangeWorker will process actor host member change request.
	membershipChangeChSize = 100

	// faultyHostDetectDuration is the maximum duration when existing host is marked as faulty.
	// Dapr runtime sends heartbeat every 1 second. Whenever placement server gets the heartbeat,
	// it updates the last heartbeat time in UpdateAt of the FSM state. If Now - UpdatedAt exceeds
	// faultyHostDetectDuration, membershipChangeWorker() tries to remove faulty Dapr runtime from
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
	// grpcServerLock is the lock fro grpcServer
	grpcServerLock *sync.Mutex
	// grpcServer is the gRPC server for placement service.
	grpcServer *grpc.Server
	// streamConnPool has the stream connections established between placement gRPC server and Dapr runtime.
	streamConnPool []placementGRPCStream
	// streamConnPoolLock is the lock for streamConnPool change.
	streamConnPoolLock *sync.RWMutex

	// raftNode is the raft server instance.
	raftNode *raft.Server

	// lastHeartBeat represents the last time stamp when runtime sent heartbeat.
	lastHeartBeat *sync.Map
	// membershipCh is the channel to maintain Dapr runtime host membership update.
	membershipCh chan hostMemberChange
	// disseminateLock is the lock for hashing table dissemination.
	disseminateLock *sync.Mutex
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

	// shutdownLock is the mutex to lock shutdown
	shutdownLock *sync.Mutex
	// shutdownCh is the channel to be used for the graceful shutdown.
	shutdownCh chan struct{}
}

// NewPlacementService returns a new placement service.
func NewPlacementService(raftNode *raft.Server) *Service {
	return &Service{
		disseminateLock:          &sync.Mutex{},
		streamConnPool:           []placementGRPCStream{},
		streamConnPoolLock:       &sync.RWMutex{},
		membershipCh:             make(chan hostMemberChange, membershipChangeChSize),
		faultyHostDetectDuration: atomic.NewInt64(int64(faultyHostDetectInitialDuration)),
		raftNode:                 raftNode,
		shutdownCh:               make(chan struct{}),
		grpcServerLock:           &sync.Mutex{},
		shutdownLock:             &sync.Mutex{},
		lastHeartBeat:            &sync.Map{},
	}
}

// Run starts the placement service gRPC server.
func (p *Service) Run(port string, certChain *dapr_credentials.CertChain) {
	var err error
	p.serverListener, err = net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	opts, err := dapr_credentials.GetServerOptions(certChain)
	if err != nil {
		log.Fatalf("error creating gRPC options: %s", err)
	}
	grpcServer := grpc.NewServer(opts...)
	placementv1pb.RegisterPlacementServer(grpcServer, p)
	p.grpcServerLock.Lock()
	p.grpcServer = grpcServer
	p.grpcServerLock.Unlock()

	if err := grpcServer.Serve(p.serverListener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Shutdown close all server connections.
func (p *Service) Shutdown() {
	p.shutdownLock.Lock()
	defer p.shutdownLock.Unlock()

	close(p.shutdownCh)

	// wait until hasLeadership is false by revokeLeadership()
	for p.hasLeadership.Load() {
		select {
		case <-time.After(5 * time.Second):
			goto TIMEOUT
		default:
			time.Sleep(500 * time.Millisecond)
		}
	}

TIMEOUT:
	p.grpcServerLock.Lock()
	if p.grpcServer != nil {
		p.grpcServer.Stop()
		p.grpcServer = nil
	}
	p.grpcServerLock.Unlock()
	p.serverListener.Close()
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(stream placementv1pb.Placement_ReportDaprStatusServer) error {
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
				p.performTablesUpdate([]placementGRPCStream{stream}, p.raftNode.FSM().PlacementState())
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
				log.Error("stream is disconnected before member is added")
				return nil
			}

			if err == io.EOF {
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
