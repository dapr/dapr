// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// MembershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) MembershipChangeWorker() {
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
				p.performTablesUpdate(p.streamConns, p.raftNode.FSM().PlacementState())
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
			log.Debugf("MembershipChangeWorker loop is closing.")
			return
		}
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// first it locks so no further dapr can be taken it then proceeds to update and
// then unlock once all runtimes have been updated
func (p *Service) performTablesUpdate(hosts []placementGRPCStream, newTable *v1pb.PlacementTables) {
	p.disseminateLock.Lock()
	defer p.disseminateLock.Unlock()

	p.disseminateOperation(hosts, "lock", nil)
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
		err = s.Send(o)
		if err != nil {
			log.Errorf("error updating host on unlock operation: %s", err)
			// TODO: the error should not be ignored. By handing error or retrying dissemination,
			// this logic needs to be improved. Otherwise, the runtimes throwing the exeception
			// will have the inconsistent hashing tables.
			continue
		}
	}

	return err
}
