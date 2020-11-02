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
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var log = logger.NewLogger("dapr.placement")

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	generation        int
	entriesLock       *sync.RWMutex
	entries           map[string]*hashing.Consistent
	hosts             []placementv1pb.Placement_ReportDaprStatusServer
	hostsEntitiesLock *sync.RWMutex
	hostsEntities     map[string][]string
	hostsLock         *sync.Mutex
	updateLock        *sync.Mutex

	raftNode *raft.Server
}

type placementOptions struct {
	incrementGeneration bool
}

// NewPlacementService returns a new placement service
func NewPlacementService(raftNode *raft.Server) *Service {
	return &Service{
		entriesLock:       &sync.RWMutex{},
		entries:           make(map[string]*hashing.Consistent),
		hostsEntitiesLock: &sync.RWMutex{},
		hostsEntities:     make(map[string][]string),
		hostsLock:         &sync.Mutex{},
		updateLock:        &sync.Mutex{},
		raftNode:          raftNode,
	}
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts
func (p *Service) ReportDaprStatus(srv placementv1pb.Placement_ReportDaprStatusServer) error {
	ctx := srv.Context()

	var registeredMemberID string

	for {
		if !p.raftNode.IsLeader() {
			return status.Error(codes.FailedPrecondition, "only leader can serve the request")
		}

		req, err := srv.Recv()
		switch err {
		case nil:
			if registeredMemberID == "" {
				registeredMemberID = req.Name
				p.addHost(ctx, srv)
				p.PerformTablesUpdate([]placementv1pb.Placement_ReportDaprStatusServer{srv},
					placementOptions{incrementGeneration: false})
				log.Debugf("New member is added: %s", registeredMemberID)
				monitoring.RecordHostsCount(len(p.hosts))
			}

			_, err := p.raftNode.ApplyCommand(raft.MemberUpsert, raft.DaprHostMember{
				Name:     req.Name,
				AppID:    req.Id,
				Entities: req.Entities,
			})
			if err != nil {
				log.Debugf("fail to apply command: %v", err)
			}

			p.ProcessHost(req)

		default:
			if registeredMemberID == "" {
				log.Debug("stream is disconnected before member is added")
				return nil
			}

			p.hostsLock.Lock()
			p.RemoveHost(srv)

			_, err := p.raftNode.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{
				Name: registeredMemberID,
			})

			if err != nil {
				log.Debugf("fail to apply command: %v", err)
			}

			// TODO: Need the robust fail-over handling by the intermittent
			// network outage to prevent from rebalancing actors.
			p.ProcessRemovedHost(registeredMemberID)
			p.hostsLock.Unlock()

			if err == io.EOF {
				log.Debugf("Member is removed gracefully: %s", registeredMemberID)
			} else {
				log.Debugf("Member is removed with error: %s, %v", registeredMemberID, err)
			}

			return nil
		}
	}
}

func (p *Service) addHost(ctx context.Context, srv placementv1pb.Placement_ReportDaprStatusServer) {
	p.hostsLock.Lock()
	defer p.hostsLock.Unlock()

	p.hosts = append(p.hosts, srv)
}

// RemoveHost removes the host from the hosts list
func (p *Service) RemoveHost(srv placementv1pb.Placement_ReportDaprStatusServer) {
	for i := len(p.hosts) - 1; i >= 0; i-- {
		if p.hosts[i] == srv {
			p.hosts = append(p.hosts[:i], p.hosts[i+1:]...)
		}
	}
}

// PerformTablesUpdate updates the connected dapr runtimes using a 3 stage commit. first it locks so no further dapr can be taken
// it then proceeds to update and then unlock once all runtimes have been updated
func (p *Service) PerformTablesUpdate(hosts []placementv1pb.Placement_ReportDaprStatusServer,
	options placementOptions) {
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

	for k, v := range p.entries {
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

// ProcessRemovedHost removes a host from the hash table
func (p *Service) ProcessRemovedHost(id string) {
	updateRequired := false

	var entities []string
	func() {
		p.hostsEntitiesLock.RLock()
		defer p.hostsEntitiesLock.RUnlock()
		entities = p.hostsEntities[id]
		delete(p.hostsEntities, id)
	}()

	func() {
		p.entriesLock.Lock()
		defer p.entriesLock.Unlock()
		for _, e := range entities {
			if _, ok := p.entries[e]; ok {
				p.entries[e].Remove(id)
				updateRequired = true
			}
		}
	}()

	if updateRequired {
		p.PerformTablesUpdate(p.hosts, placementOptions{incrementGeneration: true})
	}
}

// ProcessHost updates the distributed has list based on a new host and its entities
func (p *Service) ProcessHost(host *placementv1pb.Host) {
	updateRequired := false

	for _, e := range host.Entities {
		p.entriesLock.Lock()

		if _, ok := p.entries[e]; !ok {
			p.entries[e] = hashing.NewConsistentHash()
		}

		exists := p.entries[e].Add(host.Name, host.Id, host.Port)
		if !exists {
			updateRequired = true
			monitoring.RecordPerActorTypeReplicasCount(e, host.Name)
		}

		p.entriesLock.Unlock()
	}

	monitoring.RecordActorTypesCount(len(p.entries))
	monitoring.RecordNonActorHostsCount(len(p.hosts) - len(p.entries))

	if updateRequired {
		p.PerformTablesUpdate(p.hosts, placementOptions{incrementGeneration: true})

		p.hostsEntitiesLock.Lock()
		p.hostsEntities[host.Name] = host.Entities
		p.hostsEntitiesLock.Unlock()
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
