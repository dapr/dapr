package placement

import (
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	faultyHostDetectInterval    = 500 * time.Millisecond
	faultyHostDetectMaxDuration = 2 * time.Second
	flushTimerInterval          = 1 * time.Second
)

func (p *Service) DesseminateLoop() {
	faultHostDetectTimer := time.NewTicker(faultyHostDetectInterval)
	flushTimer := time.NewTicker(flushTimerInterval)
	lastFlushTimestamp := time.Now().UTC()

	hostUpdateCount := 0

	for {
		if !p.raftNode.IsLeader() {
			// TODO: use leader election channel instead of sleep
			// <- p.raftNode.leaderCh
			time.Sleep(100 * time.Millisecond)
			continue
		}

		select {
		case op := <-p.desseminateCh:
			switch op {
			case bufferredOperation:
				hostUpdateCount++

			case flushOperation:
				lastFlushTimestamp = time.Now().UTC()
				log.Debugf("desseminate tables to runtimes. request count: %d", hostUpdateCount)
				p.performTablesUpdate(p.streamConns, true)
				hostUpdateCount = 0
			}

		case t := <-flushTimer.C:
			if hostUpdateCount > 0 && t.Sub(lastFlushTimestamp) > flushTimerInterval {
				log.Debugf("request dessemination. request count: %d", hostUpdateCount)
				p.desseminateCh <- flushOperation
			}

		case t := <-faultHostDetectTimer.C:
			m := p.raftNode.FSM().State().Members
			tableUpdateRequired := false
			for _, v := range m {
				if t.Sub(v.UpdatedAt) < faultyHostDetectMaxDuration {
					continue
				}
				log.Debugf("try to remove hosts: %s", v.Name)
				_, err := p.raftNode.ApplyCommand(raft.MemberRemove, raft.DaprHostMember{Name: v.Name})
				if err != nil {
					log.Debugf("fail to apply command: %v", err)
				}
				tableUpdateRequired = true
			}

			if tableUpdateRequired {
				p.desseminateCh <- bufferredOperation
			}
		}
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit. first it locks so no further dapr can be taken
// it then proceeds to update and then unlock once all runtimes have been updated
func (p *Service) performTablesUpdate(hosts []placementGRPCStream, incrementGeneration bool) {
	p.updateLock.Lock()
	defer p.updateLock.Unlock()

	if incrementGeneration {
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
