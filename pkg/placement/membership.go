package placement

import (
	"strconv"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	faultyHostDetectInterval    = 500 * time.Millisecond
	faultyHostDetectMaxDuration = 3 * time.Second
	flushTimerInterval          = 1 * time.Second
)

// MembershipChangeLoop is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) MembershipChangeLoop() {
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
		case op := <-p.membershipCh:
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
				p.membershipCh <- hostMemberChange{cmdType: raft.MemberFlush}
			}

		case t := <-faultHostDetectTimer.C:
			m := p.raftNode.FSM().State().Members
			for _, v := range m {
				if t.Sub(v.UpdatedAt) < faultyHostDetectMaxDuration {
					continue
				}

				log.Debugf("try to remove hosts: %s", v.Name)

				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberRemove,
					host:    raft.DaprHostMember{Name: v.Name},
				}
			}

		case <-p.shutdownCh:
			log.Debugf("Membership change loop is closing.")
			return
		}
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// first it locks so no further dapr can be taken it then proceeds to update and
// then unlock once all runtimes have been updated
func (p *Service) performTablesUpdate(hosts []placementGRPCStream) {
	p.dessemineLock.Lock()
	defer p.dessemineLock.Unlock()

	p.disseminateOperation(hosts, "lock", nil)

	newTable := &v1pb.PlacementTables{
		Version: strconv.FormatUint(p.raftNode.FSM().State().TableGeneration, 10),
		Entries: map[string]*v1pb.PlacementTable{},
	}

	entries := p.raftNode.FSM().State().HashingTable()
	for k, v := range entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := v1pb.PlacementTable{
			Hosts:     hosts,
			SortedSet: sortedSet,
			TotalLoad: totalLoad,
			LoadMap:   make(map[string]*v1pb.Host),
		}

		for lk, lv := range loadMap {
			h := v1pb.Host{
				Name: lv.Name,
				Load: lv.Load,
				Port: lv.Port,
				Id:   lv.AppID,
			}
			table.LoadMap[lk] = &h
		}
		newTable.Entries[k] = &table
	}
	p.disseminateOperation(hosts, "update", newTable)

	p.disseminateOperation(hosts, "unlock", nil)
}

func (p *Service) disseminateOperation(targets []placementGRPCStream, operation string, tables *v1pb.PlacementTables) error {
	o := &v1pb.PlacementOrder{
		Operation: operation,
		Tables:    tables,
	}

	var err error
	for _, s := range targets {
		err := s.Send(o)
		if err != nil {
			log.Errorf("error updating host on unlock operation: %s", err)
			continue
		}
	}

	return err
}
