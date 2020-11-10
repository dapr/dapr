// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package placement

import (
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"google.golang.org/grpc/peer"
)

const (
	// raftApplyCommandMaxConcurrency is the max concurrency to apply command log to raft.
	raftApplyCommandMaxConcurrency = 10
	barrierWriteTimeout            = 2 * time.Minute
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
	// Give more time to let each runtime to find the leader and connect to the leader.
	p.faultyHostDetectDuration = faultyHostDetectInitialDuration

	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership = true
}

func (p *Service) revokeLeadership() {
	p.hasLeadership = false

	log.Info("Waiting until all connections are drained.")
	p.streamConnGroup.Wait()

	p.cleanupHeartbeats()
}

func (p *Service) cleanupHeartbeats() {
	p.lastHeartBeat.Range(func(key, value interface{}) bool {
		p.lastHeartBeat.Delete(key)
		return true
	})
}

// membershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) membershipChangeWorker(stopCh chan struct{}) {
	faultyHostDetectTimer := time.NewTicker(faultyHostDetectInterval)
	disseminateTimer := time.NewTicker(disseminateTimerInterval)

	p.memberUpdateCount.Store(0)

	go p.processRaftStateCommand(stopCh)

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

		case t := <-disseminateTimer.C:
			// Earlier stop when leadership is lost.
			if !p.hasLeadership {
				continue
			}

			// check if there is actor runtime member change.
			if p.disseminateNextTime <= t.UnixNano() && len(p.membershipCh) == 0 {
				if cnt := p.memberUpdateCount.Load(); cnt > 0 {
					log.Debugf("Add raft.TableDisseminate to membershipCh. memberUpdateCount count: %d", cnt)
					p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate}
				}
			}

		case t := <-faultyHostDetectTimer.C:
			// Earlier stop when leadership is lost.
			if !p.hasLeadership {
				continue
			}

			// Each dapr runtime sends the heartbeat every one second and placement will update UpdatedAt timestamp.
			// If UpdatedAt is outdated, we can mark the host as faulty node.
			// This faulty host will be removed from membership in the next dissemination period.
			if len(p.membershipCh) == 0 {
				m := p.raftNode.FSM().State().Members
				for _, v := range m {
					// Earlier stop when leadership is lost.
					if !p.hasLeadership {
						break
					}

					// When leader is changed and current placement node become new leader, there are no stream
					// connections from each runtime so that lastHeartBeat have no heartbeat timestamp record
					// from each runtime. Eventually, all runtime will find the leader and connect to the leader
					// of placement servers.
					// Before all runtimes connect to the leader of placements, it will record the current
					// time as heartbeat timestamp.
					heartbeat, _ := p.lastHeartBeat.LoadOrStore(v.Name, time.Now().UnixNano())

					elapsed := t.UnixNano() - heartbeat.(int64)
					if elapsed < int64(p.faultyHostDetectDuration) {
						continue
					}
					log.Debugf("Try to remove outdated host: %s, elapsed: %d ns", v.Name, elapsed)

					p.membershipCh <- hostMemberChange{
						cmdType: raft.MemberRemove,
						host:    raft.DaprHostMember{Name: v.Name},
					}
				}
			}
		}
	}
}

// processRaftStateCommand is the worker loop to apply membership change command to raft state
// and will disseminate the latest hashing table to the connected dapr runtime.
func (p *Service) processRaftStateCommand(stopCh chan struct{}) {
	// logApplyConcurrency is the buffered channel to limit the concurrency
	// of raft apply command.
	logApplyConcurrency := make(chan struct{}, raftApplyCommandMaxConcurrency)

	for {
		select {
		case <-stopCh:
			return

		case <-p.shutdownCh:
			return

		case op := <-p.membershipCh:
			switch op.cmdType {
			case raft.MemberUpsert, raft.MemberRemove:
				// MemberUpsert updates the state of dapr runtime host whenever
				// Dapr runtime sends heartbeats every 1 second.
				// MemberRemove will be queued by faultHostDetectTimer.
				// Even if ApplyCommand is failed, both commands will retry
				// until the state is consistent.
				logApplyConcurrency <- struct{}{}
				go func() {
					updated, raftErr := p.raftNode.ApplyCommand(op.cmdType, op.host)
					if raftErr != nil {
						log.Errorf("fail to apply command: %v", raftErr)
					} else {
						if op.cmdType == raft.MemberRemove {
							p.lastHeartBeat.Delete(op.host.Name)
						}

						// ApplyCommand returns true only if the command changes hashing table.
						if updated {
							p.memberUpdateCount.Inc()
							// disseminateNextTime will be updated whenever apply is done, so that
							// it will keep moving the time to disseminate the table, which will
							// reduce the unnecessary table dissemination.
							p.disseminateNextTime = time.Now().Add(disseminateTimeout).UnixNano()
						}
					}
					<-logApplyConcurrency
				}()

			case raft.TableDisseminate:
				// TableDissminate will be triggered by disseminateTimer.
				// This disseminates the latest consistent hashing tables to Dapr runtime.
				nStreamConnPool := len(p.streamConnPool)
				nTargetConns := len(p.raftNode.FSM().State().Members)

				monitoring.RecordRuntimesCount(nStreamConnPool)
				monitoring.RecordActorRuntimesCount(nTargetConns)

				// ignore dissemination if there is no member update.
				if cnt := p.memberUpdateCount.Load(); cnt > 0 {
					state := p.raftNode.FSM().PlacementState()
					log.Infof(
						"Start desseminating tables. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
						cnt, nStreamConnPool, nTargetConns, state.Version)
					p.performTablesUpdate(p.streamConnPool, state)
					log.Infof(
						"Completed dessemination. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
						cnt, nStreamConnPool, nTargetConns, state.Version)
					p.memberUpdateCount.Store(0)

					// set faultyHostDetectDuration to the default duration.
					p.faultyHostDetectDuration = faultyHostDetectDefaultDuration
				}
			}
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
			remoteAddr := "n/a"
			if peer, ok := peer.FromContext(s.Context()); ok {
				remoteAddr = peer.Addr.String()
			}

			log.Errorf("error updating runtime host (%q) on %q operation: %s", remoteAddr, operation, err)
			// TODO: the error should not be ignored. By handing error or retrying dissemination,
			// this logic needs to be improved. Otherwise, the runtimes throwing the exeception
			// will have the inconsistent hashing tables.
		}
	}

	return err
}
