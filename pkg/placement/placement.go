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
	membershipChangeChSize = 100
)

type hostMemberChange struct {
	cmdType raft.CommandType
	host    raft.DaprHostMember
}

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	serverListener net.Listener
	grpcServer     *grpc.Server

	raftNode *raft.Server

	streamConns     []placementGRPCStream
	streamConnsLock *sync.Mutex

	membershipCh  chan hostMemberChange
	dessemineLock *sync.Mutex

	shutdownCh chan struct{}
}

// NewPlacementService returns a new placement service.
func NewPlacementService(raftNode *raft.Server) *Service {
	return &Service{
		dessemineLock:   &sync.Mutex{},
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
				p.performTablesUpdate([]placementGRPCStream{srv})
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
				// no actions for hashing table. instead, connectionMonitoring will check
				// host updatedAt and if current - updatedAt > 2 seconds, remove hosts
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
