// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

const (
	commandRetryInterval = 20 * time.Millisecond
	commandRetryMaxCount = 3

	barrierWriteTimeout = 2 * time.Minute
)

// MonitorLeadership is used to monitor if we acquire or lose our role
// as the leader in the Raft cluster. There is some work the leader is
// expected to do, so we must react to changes
//
// reference: https://github.com/hashicorp/consul/blob/master/agent/consul/leader.go
func (p *Service) MonitorLeadership() {
	var weAreLeaderCh chan struct{}
	var leaderLoop sync.WaitGroup

	leaderCh := p.raftNode.Raft().LeaderCh()

	for {
		select {
		case isLeader := <-leaderCh:
			if isLeader {
				if weAreLeaderCh != nil {
					log.Error("attempted to start the leader loop while running")
					continue
				}

				weAreLeaderCh = make(chan struct{})
				leaderLoop.Add(1)
				go func(ch chan struct{}) {
					defer leaderLoop.Done()
					p.leaderLoop(ch)
				}(weAreLeaderCh)
				log.Info("cluster leadership acquired")
			} else {
				if weAreLeaderCh == nil {
					log.Error("attempted to stop the leader loop while not running")
					continue
				}

				log.Info("shutting down leader loop")
				close(weAreLeaderCh)
				leaderLoop.Wait()
				weAreLeaderCh = nil
				log.Info("cluster leadership lost")
			}

		case <-p.shutdownCh:
			return
		}
	}
}

func (p *Service) leaderLoop(stopCh chan struct{}) {
	// This loop is to ensure the FSM reflects all queued writes by applying Barrier
	// and completes leadership establishment before becoming a leader.
	for !p.hasLeadership {
		// for earlier stop
		select {
		case <-stopCh:
			return
		case <-p.shutdownCh:
			return
		default:
		}

		barrier := p.raftNode.Raft().Barrier(barrierWriteTimeout)
		if err := barrier.Error(); err != nil {
			log.Error("failed to wait for barrier", "error", err)
			continue
		}

		if !p.hasLeadership {
			p.establishLeadership()
			log.Info("leader is established.")
			// revoke leadership process must be done before leaderLoop() ends.
			defer p.revokeLeadership()
		}
	}

	p.membershipChangeWorker(stopCh)
}

func (p *Service) establishLeadership() {
	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership = true
}

func (p *Service) revokeLeadership() {
	p.hasLeadership = false

	log.Info("Waiting until all connections are drained.")
	p.streamConnGroup.Wait()
}

// membershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) membershipChangeWorker(stopCh chan struct{}) {
	faultyHostDetectTimer := time.NewTicker(faultyHostDetectInterval)
	disseminateTimer := time.NewTicker(disseminateTimerInterval)

	p.memberUpdateCount = 0

	for {
		select {
		case <-stopCh:
			faultyHostDetectTimer.Stop()
			disseminateTimer.Stop()
			return

		case <-p.shutdownCh:
			faultyHostDetectTimer.Stop()
			disseminateTimer.Stop()
			return

		case op := <-p.membershipCh:
			p.processRaftStateCommand(op)

		case <-disseminateTimer.C:
			// check if there is actor runtime member change.
			if p.memberUpdateCount > 0 {
				log.Debugf("request dessemination. memberUpdateCount count: %d", p.memberUpdateCount)
				p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate}
			}

		case t := <-faultyHostDetectTimer.C:
			// Each dapr runtime sends the heartbeat every one second and placement will update UpdatedAt timestamp.
			// If UpdatedAt is outdated, we can mark the host as faulty node.
			// This faulty host will be removed from membership in the next dissemination period.
			m := p.raftNode.FSM().State().Members
			for _, v := range m {
				if t.Sub(v.UpdatedAt) < faultyHostDetectMaxDuration {
					continue
				}
				log.Debugf("try to remove outdated hosts: %s, elapsed: %d ms", v.Name, t.Sub(v.UpdatedAt).Milliseconds())

				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberRemove,
					host:    raft.DaprHostMember{Name: v.Name},
				}
			}
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
			p.memberUpdateCount++
		}

	case raft.TableDisseminate:
		// TableDissminate will be triggered by disseminateTimer.
		// This disseminates the latest consistent hashing tables to Dapr runtime.

		// Disseminate hashing tables to Dapr runtimes only if number of current stream connections
		// and number of FSM state members are matched. Otherwise, this will be retried until
		// the numbers are matched.

		// When placement node first gets the leadership, it needs to wait until runtimes connecting
		// old leader connects to new leader. The numbers will be eventually consistent.
		streamConns := len(p.streamConns)
		targetConns := len(p.raftNode.FSM().State().Members)
		if streamConns == targetConns {
			log.Debugf(
				"desseminate tables to memers. memberUpdateCount: %d, streams: %d, targets: %d",
				p.memberUpdateCount, streamConns, targetConns)
			p.performTablesUpdate(p.streamConns, p.raftNode.FSM().PlacementState())
			p.memberUpdateCount = 0
		}
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(hosts []placementGRPCStream, newTable *v1pb.PlacementTables) {
	p.disseminateLock.Lock()
	defer p.disseminateLock.Unlock()

	// TODO: error from disseminationOperation needs to be handle properly.
	// Otherwise, each Dapr runtime will have inconsistent hashing table.
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
