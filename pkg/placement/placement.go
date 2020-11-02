// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var log = logger.NewLogger("dapr.placement")

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer

const (
	// membershipChangeChSize is the channel size of membership change request from Dapr runtime.
	// MembershipChangeWorker will process actor host member change request.
	membershipChangeChSize = 100

	// faultyHostDetectMaxDuration is the maximum duration when existing host is marked as faulty.
	// Dapr runtime sends heartbeat every 1 second. Whenever placement server gets the heartbeat,
	// it updates the last heartbeat time in UpdateAt of the FSM state. If Now - UpdatedAt exceeds
	// faultyHostDetectMaxDuration, MembershipChangeWorker tries to remove faulty dapr runtime from
	// membership.
	faultyHostDetectMaxDuration = 3 * time.Second
	// faultyHostDetectInterval is the interval to check the faulty member.
	faultyHostDetectInterval = 500 * time.Millisecond

	// flushTimerInterval is the interval to disseminate the latest consistent hashing table.
	flushTimerInterval = 500 * time.Millisecond
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
	// streamConns has the stream connections established between placement gRPC server and Dapr runtime.
	streamConns []placementGRPCStream
	// streamConnsLock is the lock for streamConns change.
	streamConnsLock *sync.Mutex

	// raftNode is the raft server instance.
	raftNode *raft.Server

	// membershipCh is the channel to maintain Dapr runtime host membership update.
	membershipCh chan hostMemberChange
	// disseminateLock is the lock for hashing table dissemination.
	disseminateLock *sync.Mutex

	// shutdownCh is the channel to be used for the graceful shutdown.
	shutdownCh chan struct{}
}

// NewPlacementService returns a new placement service.
func NewPlacementService(raftNode *raft.Server) *Service {
	return &Service{
		disseminateLock: &sync.Mutex{},
		streamConns:     []placementGRPCStream{},
		streamConnsLock: &sync.Mutex{},
		membershipCh:    make(chan hostMemberChange, membershipChangeChSize),
		raftNode:        raftNode,
		shutdownCh:      make(chan struct{}),
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
	p.grpcServer = grpc.NewServer(opts...)
	placementv1pb.RegisterPlacementServer(p.grpcServer, p)

	if err := p.grpcServer.Serve(p.serverListener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// Shutdown close all server connections.
func (p *Service) Shutdown() {
	if p.grpcServer != nil {
		p.grpcServer.Stop()
	}
	p.serverListener.Close()
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(srv placementv1pb.Placement_ReportDaprStatusServer) error {
	ctx := srv.Context()

	registeredMemberID := ""

	for {
		if !p.raftNode.IsLeader() {
			return status.Error(codes.FailedPrecondition, "only leader can serve the request")
		}

		req, err := srv.Recv()
		switch err {
		case nil:
			if registeredMemberID == "" {
				registeredMemberID = req.Name
				p.addRuntimeConnection(ctx, srv)
				p.performTablesUpdate([]placementGRPCStream{srv}, p.raftNode.FSM().PlacementState())
				log.Debugf("Stream connection is establiched from %s", registeredMemberID)
			}

			p.membershipCh <- hostMemberChange{
				cmdType: raft.MemberUpsert,
				host: raft.DaprHostMember{
					Name:     req.Name,
					AppID:    req.Id,
					Entities: req.Entities,
				},
			}

		default:
			if registeredMemberID == "" {
				log.Error("stream is disconnected before member is added")
				return nil
			}

			p.deleteRuntimeConnection(srv)
			if err == io.EOF {
				log.Debugf("Stream connection is disconnected gracefully: %s", registeredMemberID)
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberRemove,
					host:    raft.DaprHostMember{Name: registeredMemberID},
				}
			} else {
				// no actions for hashing table. Instead, MembershipChangeWorker will check
				// host updatedAt and if now - updatedAt > faultyHostDetectMaxDuration, remove hosts.
				log.Debugf("Stream connection is disconnected with the error: %v", err)
			}

			return nil
		}
	}
}

func (p *Service) addRuntimeConnection(ctx context.Context, conn placementGRPCStream) {
	p.streamConnsLock.Lock()
	p.streamConns = append(p.streamConns, conn)
	p.streamConnsLock.Unlock()
}

func (p *Service) deleteRuntimeConnection(conn placementGRPCStream) {
	p.streamConnsLock.Lock()
	for i, c := range p.streamConns {
		if c == conn {
			p.streamConns = append(p.streamConns[:i], p.streamConns[i+1:]...)
			break
		}
	}
	p.streamConnsLock.Unlock()
}
