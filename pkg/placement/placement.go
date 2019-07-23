package placement

import (
	"errors"
	"fmt"
	"net"
	"sync"

	log "github.com/Sirupsen/logrus"
	pb "github.com/actionscore/actions/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type PlacementService struct {
	generation        int
	entriesLock       *sync.RWMutex
	entries           map[string]*Consistent
	hosts             []pb.PlacementService_ReportActionStatusServer
	hostsEntitiesLock *sync.RWMutex
	hostsEntities     map[string][]string
	hostsLock         *sync.Mutex
	updateLock        *sync.Mutex
}

func NewPlacementService() *PlacementService {
	return &PlacementService{
		entriesLock:       &sync.RWMutex{},
		entries:           make(map[string]*Consistent),
		hostsEntitiesLock: &sync.RWMutex{},
		hostsEntities:     make(map[string][]string),
		hostsLock:         &sync.Mutex{},
		updateLock:        &sync.Mutex{},
	}
}

func (p *PlacementService) ReportActionStatus(srv pb.PlacementService_ReportActionStatusServer) error {
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

func (p *PlacementService) RemoveHost(srv pb.PlacementService_ReportActionStatusServer) {
	for i := len(p.hosts) - 1; i >= 0; i-- {
		if p.hosts[i] == srv {
			p.hosts = append(p.hosts[:i], p.hosts[i+1:]...)
		}
	}
}

func (p *PlacementService) PerformTablesUpdate() {
	p.updateLock.Lock()
	defer p.updateLock.Unlock()

	p.generation++
	o := pb.PlacementOrder{
		Operation: "lock",
	}

	for _, host := range p.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on lock operation: %s", err)
			continue
		}
	}

	v := fmt.Sprintf("%v", p.generation)

	o.Operation = "update"
	o.Tables = &pb.PlacementTables{
		Version: v,
		Entries: map[string]*pb.PlacementTable{},
	}

	for k, v := range p.entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := pb.PlacementTable{
			Hosts:     hosts,
			SortedSet: sortedSet,
			TotalLoad: totalLoad,
			LoadMap:   make(map[string]*pb.Host),
		}

		for lk, lv := range loadMap {
			h := pb.Host{
				Name: lv.Name,
				Load: lv.Load,
				Port: lv.Port,
			}

			table.LoadMap[lk] = &h
		}

		o.Tables.Entries[k] = &table
	}

	for _, host := range p.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on update operation: %s", err)
			continue
		}
	}

	o.Tables = nil
	o.Operation = "unlock"

	for _, host := range p.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("error updating host on unlock operation: %s", err)
			continue
		}
	}
}

func (p *PlacementService) ProcessRemovedHost(id string) {
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
		p.PerformTablesUpdate()
	}
}

func (p *PlacementService) ProcessHost(host *pb.Host) {
	updateRequired := false

	for _, e := range host.Entities {
		p.entriesLock.Lock()
		if _, ok := p.entries[e]; !ok {
			p.entries[e] = NewConsistentHash()
		}

		exists := p.entries[e].Add(host.Name, host.Port)
		if !exists {
			updateRequired = true
		}
		p.entriesLock.Unlock()
	}

	if updateRequired {
		p.PerformTablesUpdate()
	}

	p.hostsEntitiesLock.Lock()
	p.hostsEntities[host.Name] = host.Entities
	p.hostsEntitiesLock.Unlock()
}

func (p *PlacementService) Run(port string) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterPlacementServiceServer(s, p)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
