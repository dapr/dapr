// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/dapr/dapr/pkg/logger"
	daprinternal_pb "github.com/dapr/dapr/pkg/proto/daprinternal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var log = logger.NewLogger("dapr.placement")

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	generation        int
	entriesLock       *sync.RWMutex
	entries           map[string]*Consistent
	hosts             []daprinternal_pb.PlacementService_ReportDaprStatusServer
	hostsEntitiesLock *sync.RWMutex
	hostsEntities     map[string][]string
	hostsLock         *sync.Mutex
	updateLock        *sync.Mutex
}

type placementOptions struct {
	incrementGeneration bool
}

// NewPlacementService returns a new placement service
func NewPlacementService() *Service {
	return &Service{
		entriesLock:       &sync.RWMutex{},
		entries:           make(map[string]*Consistent),
		hostsEntitiesLock: &sync.RWMutex{},
		hostsEntities:     make(map[string][]string),
		hostsLock:         &sync.Mutex{},
		updateLock:        &sync.Mutex{},
	}
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts
func (p *Service) ReportDaprStatus(srv daprinternal_pb.PlacementService_ReportDaprStatusServer) error {
	ctx := srv.Context()
	p.hostsLock.Lock()
	md, _ := metadata.FromIncomingContext(srv.Context())
	v := md.Get("id")
	if len(v) == 0 {
		return errors.New("id header not found in metadata")
	}

	id := v[0]
	p.hosts = append(p.hosts, srv)
	log.Infof("host added: %s", id)
	p.hostsLock.Unlock()

	// send the current placements
	p.PerformTablesUpdate([]daprinternal_pb.PlacementService_ReportDaprStatusServer{srv},
		placementOptions{incrementGeneration: false})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := srv.Recv()
		if err != nil {
			p.hostsLock.Lock()
			p.RemoveHost(srv)
			p.ProcessRemovedHost(id)
			log.Infof("host removed: %s", id)
			p.hostsLock.Unlock()
			continue
		}

		p.ProcessHost(req)
	}
}

// RemoveHost removes the host from the hosts list
func (p *Service) RemoveHost(srv daprinternal_pb.PlacementService_ReportDaprStatusServer) {
	for i := len(p.hosts) - 1; i >= 0; i-- {
		if p.hosts[i] == srv {
			p.hosts = append(p.hosts[:i], p.hosts[i+1:]...)
		}
	}
}

// PerformTablesUpdate updates the connected dapr runtimes using a 3 stage commit. first it locks so no further dapr can be taken
// it then proceeds to update and then unlock once all runtimes have been updated
func (p *Service) PerformTablesUpdate(hosts []daprinternal_pb.PlacementService_ReportDaprStatusServer,
	options placementOptions) {
	p.updateLock.Lock()
	defer p.updateLock.Unlock()

	if options.incrementGeneration {
		p.generation++
	}

	o := daprinternal_pb.PlacementOrder{
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
	o.Tables = &daprinternal_pb.PlacementTables{
		Version: v,
		Entries: map[string]*daprinternal_pb.PlacementTable{},
	}

	for k, v := range p.entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := daprinternal_pb.PlacementTable{
			Hosts:     hosts,
			SortedSet: sortedSet,
			TotalLoad: totalLoad,
			LoadMap:   make(map[string]*daprinternal_pb.Host),
		}

		for lk, lv := range loadMap {
			h := daprinternal_pb.Host{
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

	p.hostsEntitiesLock.RLock()
	entities := p.hostsEntities[id]
	delete(p.hostsEntities, id)
	p.hostsEntitiesLock.RUnlock()

	p.entriesLock.Lock()
	for _, e := range entities {
		if _, ok := p.entries[e]; ok {
			p.entries[e].Remove(id)
			updateRequired = true
		}
	}
	p.entriesLock.Unlock()

	if updateRequired {
		p.PerformTablesUpdate(p.hosts, placementOptions{incrementGeneration: true})
	}
}

// ProcessHost updates the distributed has list based on a new host and its entities
func (p *Service) ProcessHost(host *daprinternal_pb.Host) {
	updateRequired := false

	for _, e := range host.Entities {
		p.entriesLock.Lock()
		if _, ok := p.entries[e]; !ok {
			p.entries[e] = NewConsistentHash()
		}

		exists := p.entries[e].Add(host.Name, host.Id, host.Port)
		if !exists {
			updateRequired = true
		}
		p.entriesLock.Unlock()
	}

	if updateRequired {
		p.PerformTablesUpdate(p.hosts, placementOptions{incrementGeneration: true})
	}

	p.hostsEntitiesLock.Lock()
	p.hostsEntities[host.Name] = host.Entities
	p.hostsEntitiesLock.Unlock()
}

// Run starts the placement service gRPC server
func (p *Service) Run(port string) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	daprinternal_pb.RegisterPlacementServiceServer(s, p)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
