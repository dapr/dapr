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

type desseminateOp int

const (
	bufferredOperation desseminateOp = iota
	flushOperation
)

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	generation int
	updateLock *sync.Mutex

	desseminateCh   chan desseminateOp
	streamConns     []placementGRPCStream
	streamConnsLock *sync.Mutex

	raftNode *raft.Server
}

type placementOptions struct {
	incrementGeneration bool
}

// NewPlacementService returns a new placement service
func NewPlacementService(raftNode *raft.Server) *Service {
	return &Service{
		updateLock:      &sync.Mutex{},
		streamConnsLock: &sync.Mutex{},
		desseminateCh:   make(chan desseminateOp, 100),
		raftNode:        raftNode,
	}
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts
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
				p.addConn(ctx, srv)
				log.Debugf("New member is added: %s", registeredMemberID)
			}

			_, err := p.raftNode.ApplyCommand(raft.MemberUpsert, raft.DaprHostMember{
				Name:     req.Name,
				AppID:    req.Id,
				Entities: req.Entities,
			})
			if err != nil {
				log.Debugf("fail to apply command: %v", err)
				continue
			}

			p.desseminateCh <- bufferredOperation

		default:
			if registeredMemberID == "" {
				log.Debug("stream is disconnected before member is added")
				return nil
			}

			p.deleteConn(srv)
			if err == io.EOF {
				log.Debugf("Member is removed gracefully: %s", registeredMemberID)
				// Remove member and desseminate tables immediately
				// do batched remove and dessemination
				_, err := p.raftNode.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
					Name: registeredMemberID,
				})
				if err != nil {
					log.Debugf("fail to apply command: %v", err)
					continue
				}

				p.desseminateCh <- bufferredOperation
			} else {
				// no actions for hashing table. instead, connectionMonitoring will check
				// host updatedAt and if current - updatedAt > 2 seconds, remove hosts
				log.Debugf("Member is removed with error: %s, %v", registeredMemberID, err)
			}

			return nil
		}
	}
}

func (p *Service) addConn(ctx context.Context, conn placementGRPCStream) {
	p.streamConnsLock.Lock()
	p.streamConns = append(p.streamConns, conn)
	p.streamConnsLock.Unlock()
}

func (p *Service) deleteConn(conn placementGRPCStream) {
	p.streamConnsLock.Lock()
	for i, c := range p.streamConns {
		if c == conn {
			p.streamConns = append(p.streamConns[:i], p.streamConns[i+1:]...)
			break
		}
	}
	p.streamConnsLock.Unlock()
}

// PerformTablesUpdate updates the connected dapr runtimes using a 3 stage commit. first it locks so no further dapr can be taken
// it then proceeds to update and then unlock once all runtimes have been updated
func (p *Service) PerformTablesUpdate(hosts []placementGRPCStream, options placementOptions) {
	p.updateLock.Lock()
	defer p.updateLock.Unlock()

	if options.incrementGeneration {
		p.generation++
	}

	o := placementv1pb.PlacementOrder{
		Operation: "lock",
	}

	for _, host := range hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on lock operation: %s", err)
			continue
		}
	}

	v := fmt.Sprintf("%v", p.generation)

	o.Operation = "update"
	o.Tables = &placementv1pb.PlacementTables{
		Version: v,
		Entries: map[string]*placementv1pb.PlacementTable{},
	}

	entries := p.raftNode.FSM().State().HashingTable()

	for k, v := range entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := placementv1pb.PlacementTable{
			Hosts:     hosts,
			SortedSet: sortedSet,
			TotalLoad: totalLoad,
			LoadMap:   make(map[string]*placementv1pb.Host),
		}

		for lk, lv := range loadMap {
			h := placementv1pb.Host{
				Name: lv.Name,
				Load: lv.Load,
				Port: lv.Port,
				Id:   lv.AppID,
			}
			table.LoadMap[lk] = &h
		}
		o.Tables.Entries[k] = &table
	}

	for _, host := range hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on update operation: %s", err)
			continue
		}
	}

	o.Tables = nil
	o.Operation = "unlock"

	for _, host := range hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on unlock operation: %s", err)
			continue
		}
	}
}

// Run starts the placement service gRPC server
func (p *Service) Run(port string, certChain *dapr_credentials.CertChain) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	opts, err := dapr_credentials.GetServerOptions(certChain)
	if err != nil {
		log.Fatalf("error creating gRPC options: %s", err)
	}
	s := grpc.NewServer(opts...)
	placementv1pb.RegisterPlacementServer(s, p)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
