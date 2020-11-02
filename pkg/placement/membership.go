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

const (
	commandRetryInterval = 100 * time.Millisecond
	commandRetryMaxCount = 3
)

// MembershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) MembershipChangeWorker() {
	faultHostDetectTimer := time.NewTicker(faultyHostDetectInterval)
	disseminateTimer := time.NewTicker(disseminateTimerInterval)

	p.hostUpdateCount = 0

	for {
		if !p.raftNode.IsLeader() {
			// TODO: use leader election channel instead of sleep
			// <- p.raftNode.leaderCh
			time.Sleep(100 * time.Millisecond)
			continue
		}

		select {
		case op := <-p.membershipCh:
			p.processRaftStateCommand(op)

		case <-disseminateTimer.C:
			// check if there is actor runtime member change.
			if p.hostUpdateCount > 0 {
				log.Debugf("request dessemination. hostUpdateCount count: %d", p.hostUpdateCount)
				p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate}
			}

		case t := <-faultHostDetectTimer.C:
			// Each dapr runtime sends the heartbeat every one second and placement will update UpdatedAt timestamp.
			// If UpdatedAt is outdated, we can mark the host as faulty node.
			// This faulty host will be removed from membership in the next dissemination period.
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

func (p *Service) processRaftStateCommand(op hostMemberChange) {
	switch op.cmdType {
	case raft.MemberUpsert, raft.MemberRemove:
		// MemberUpsert updates the state of dapr runtime host whenever
		// Dapr runtime sends heartbeats every 1 second.
		// MemberRemove will be queued by faultHostDetectTimer.
		// Even if ApplyCommand is failed, both commands will retry
		// until the state is consistent.
		var raftErr error
		updated := false

		for retry := 0; retry < commandRetryMaxCount; retry++ {
			updated, raftErr = p.raftNode.ApplyCommand(op.cmdType, op.host)
			if raftErr == nil {
				break
			} else {
				log.Debugf("fail to apply command: %v, retry: %d", raftErr, retry)
			}
			time.Sleep(commandRetryInterval)
		}

		if raftErr != nil {
			log.Errorf("fail to apply command: %v", raftErr)
		}

		if updated {
			p.hostUpdateCount++
		}

	case raft.TableDisseminate:
		// TableDissminate will be triggered by disseminateTimer.
		// This will disseminate the latest consisten hashing tables to Dapr runtime.
		if len(p.streamConns) > 0 {
			log.Debugf("desseminate tables to runtimes. hostUpdateCount count: %d", p.hostUpdateCount)
			p.performTablesUpdate(p.streamConns, p.raftNode.FSM().PlacementState())
		}
		p.hostUpdateCount = 0
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
