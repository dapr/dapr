package assigner

import (
	"fmt"
	"net"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/consistenthash"
	pb "github.com/actionscore/actions/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type Assigner struct {
	generation        int
	entriesLock       *sync.RWMutex
	entries           map[string]*consistenthash.Consistent
	hosts             []pb.Assigner_ReportActionStatusServer
	hostsEntitiesLock *sync.RWMutex
	hostsEntities     map[string][]string
	hostsLock         *sync.Mutex
	updateLock        *sync.Mutex
}

func NewAssigner() *Assigner {
	return &Assigner{
		entriesLock:       &sync.RWMutex{},
		entries:           make(map[string]*consistenthash.Consistent),
		hostsEntitiesLock: &sync.RWMutex{},
		hostsEntities:     make(map[string][]string),
		hostsLock:         &sync.Mutex{},
		updateLock:        &sync.Mutex{},
	}
}

func (a *Assigner) ReportActionStatus(srv pb.Assigner_ReportActionStatusServer) error {
	ctx := srv.Context()
	a.hostsLock.Lock()
	md, _ := metadata.FromIncomingContext(srv.Context())
	id := md["id"][0]
	a.hosts = append(a.hosts, srv)
	log.Infof("host added: %s", id)
	a.hostsLock.Unlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := srv.Recv()
		if err != nil {
			a.hostsLock.Lock()
			a.RemoveHost(srv)
			a.ProcessRemovedHost(id)
			log.Infof("host removed: %s", id)
			a.hostsLock.Unlock()
			continue
		}

		a.ProcessHost(req)
	}
}

func (a *Assigner) RemoveHost(srv pb.Assigner_ReportActionStatusServer) {
	for i := len(a.hosts) - 1; i >= 0; i-- {
		if a.hosts[i] == srv {
			a.hosts = append(a.hosts[:i], a.hosts[i+1:]...)
		}
	}
}

func (a *Assigner) PerformTablesUpdate() {
	a.updateLock.Lock()
	defer a.updateLock.Unlock()

	a.generation++
	o := pb.AssignerOrder{
		Operation: "lock",
	}

	for _, host := range a.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("Error updating host on lock operation: %s", err)
			continue
		}
	}

	v := fmt.Sprintf("%v", a.generation)

	o.Operation = "update"
	o.Tables = &pb.AssignmentTables{
		Version: v,
		Entries: map[string]*pb.AssignmentTable{},
	}

	for k, v := range a.entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := pb.AssignmentTable{
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

	for _, host := range a.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("Error updating host on update operation: %s", err)
			continue
		}
	}

	o.Tables = nil
	o.Operation = "unlock"

	for _, host := range a.hosts {
		err := host.Send(&o)
		if err != nil {
			log.Errorf("Error updating host on unlock operation: %s", err)
			continue
		}
	}
}

func (a *Assigner) ProcessRemovedHost(id string) {
	updateRequired := false

	a.hostsEntitiesLock.RLock()
	entities := a.hostsEntities[id]
	delete(a.hostsEntities, id)
	a.hostsEntitiesLock.RUnlock()

	a.entriesLock.Lock()
	for _, e := range entities {
		if _, ok := a.entries[e]; ok {
			a.entries[e].Remove(id)
			updateRequired = true
		}
	}
	a.entriesLock.Unlock()

	if updateRequired {
		a.PerformTablesUpdate()
	}
}

func (a *Assigner) ProcessHost(host *pb.Host) {
	updateRequired := false

	for _, e := range host.Entities {
		a.entriesLock.Lock()
		if _, ok := a.entries[e]; !ok {
			a.entries[e] = consistenthash.New()
		}

		exists := a.entries[e].Add(host.Name, host.Port)
		if !exists {
			updateRequired = true
		}
		a.entriesLock.Unlock()
	}

	if updateRequired {
		a.PerformTablesUpdate()
	}

	a.hostsEntitiesLock.Lock()
	a.hostsEntities[host.Name] = host.Entities
	a.hostsEntitiesLock.Unlock()
}

func (a *Assigner) Run(port string) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterAssignerServer(s, a)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
