package placement

import (
	"strconv"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	faultyHostDetectInterval    = 500 * time.Millisecond
	faultyHostDetectMaxDuration = 3 * time.Second
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
			switch op.cmdType {
			case raft.MemberUpsert:
				fallthrough

			case raft.MemberRemove:
				updated, raftErr := p.raftNode.ApplyCommand(op.cmdType, op.host)
				if raftErr != nil {
					log.Debugf("fail to apply command: %v", raftErr)
				}
				if updated {
					hostUpdateCount++
				}

			case raft.MemberFlush:
				lastFlushTimestamp = time.Now().UTC()
				hostUpdateCount = 0

				if len(p.streamConns) == 0 {
					break
				}

				log.Debugf("desseminate tables to runtimes. request count: %d", hostUpdateCount)
				p.performTablesUpdate(p.streamConns)
			}

		case t := <-flushTimer.C:
			if hostUpdateCount > 0 && t.Sub(lastFlushTimestamp) > flushTimerInterval {
				log.Debugf("request dessemination. request count: %d", hostUpdateCount)
				p.desseminateCh <- hostMemberCommand{cmdType: raft.MemberFlush}
			}

		case t := <-faultHostDetectTimer.C:
			m := p.raftNode.FSM().State().Members
			for _, v := range m {
				if t.Sub(v.UpdatedAt) < faultyHostDetectMaxDuration {
					continue
				}

				log.Debugf("try to remove hosts: %s", v.Name)

				p.desseminateCh <- hostMemberCommand{
					cmdType: raft.MemberRemove,
					host:    raft.DaprHostMember{Name: v.Name},
				}
			}
		}
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit. first it locks so no further dapr can be taken
// it then proceeds to update and then unlock once all runtimes have been updated
func (p *Service) performTablesUpdate(hosts []placementGRPCStream) {
	p.updateLock.Lock()
	defer p.updateLock.Unlock()

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

	o.Operation = "update"
	o.Tables = &placementv1pb.PlacementTables{
		Version: strconv.FormatUint(p.raftNode.FSM().State().TableGeneration, 10),
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
