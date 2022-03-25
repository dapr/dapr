/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package placement

import (
	"sync"
	"time"

	"google.golang.org/grpc/peer"

	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"

	"github.com/dapr/kit/retry"
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
	for !p.hasLeadership.Load() {
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

		if !p.hasLeadership.Load() {
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
	p.faultyHostDetectDuration.Store(int64(faultyHostDetectInitialDuration))

	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership.Store(true)
}

func (p *Service) revokeLeadership() {
	p.hasLeadership.Store(false)

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
			if !p.hasLeadership.Load() {
				continue
			}

			// check if there is actor runtime member change.
			if p.disseminateNextTime.Load() <= t.UnixNano() && len(p.membershipCh) == 0 {
				if cnt := p.memberUpdateCount.Load(); cnt > 0 {
					log.Debugf("Add raft.TableDisseminate to membershipCh. memberUpdateCount count: %d", cnt)
					p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate}
				}
			}

		case t := <-faultyHostDetectTimer.C:
			// Earlier stop when leadership is lost.
			if !p.hasLeadership.Load() {
				continue
			}

			// Each dapr runtime sends the heartbeat every one second and placement will update UpdatedAt timestamp.
			// If UpdatedAt is outdated, we can mark the host as faulty node.
			// This faulty host will be removed from membership in the next dissemination period.
			if len(p.membershipCh) == 0 {
				m := p.raftNode.FSM().State().Members()
				for _, v := range m {
					// Earlier stop when leadership is lost.
					if !p.hasLeadership.Load() {
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
					if elapsed < p.faultyHostDetectDuration.Load() {
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

	processingTicker := time.NewTicker(10 * time.Second)

	for {
		select {
		case <-stopCh:
			return

		case <-p.shutdownCh:
			return

		case <-processingTicker.C:
			log.Debugf("process raft state command: nothing happened...")
			continue

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
					// We lock dissemination to ensure the updates can complete before the table is disseminated.
					p.disseminateLock.Lock()
					defer p.disseminateLock.Unlock()

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
							p.disseminateNextTime.Store(time.Now().Add(disseminateTimeout).UnixNano())
						}
					}
					<-logApplyConcurrency
				}()

			case raft.TableDisseminate:
				// TableDisseminate will be triggered by disseminateTimer.
				// This disseminates the latest consistent hashing tables to Dapr runtime.
				p.performTableDissemination()
			}
		}
	}
}

func (p *Service) performTableDissemination() {
	p.streamConnPoolLock.RLock()
	nStreamConnPool := len(p.streamConnPool)
	p.streamConnPoolLock.RUnlock()
	nTargetConns := len(p.raftNode.FSM().State().Members())

	monitoring.RecordRuntimesCount(nStreamConnPool)
	monitoring.RecordActorRuntimesCount(nTargetConns)

	// ignore dissemination if there is no member update.
	if cnt := p.memberUpdateCount.Load(); cnt > 0 {
		p.disseminateLock.Lock()
		defer p.disseminateLock.Unlock()

		state := p.raftNode.FSM().PlacementState()
		log.Infof(
			"Start disseminating tables. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
			cnt, nStreamConnPool, nTargetConns, state.Version)
		p.streamConnPoolLock.RLock()
		streamConnPool := make([]placementGRPCStream, len(p.streamConnPool))
		copy(streamConnPool, p.streamConnPool)
		p.streamConnPoolLock.RUnlock()
		p.performTablesUpdate(streamConnPool, state)
		log.Infof(
			"Completed dissemination. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
			cnt, nStreamConnPool, nTargetConns, state.Version)
		p.memberUpdateCount.Store(0)

		// set faultyHostDetectDuration to the default duration.
		p.faultyHostDetectDuration.Store(int64(faultyHostDetectDefaultDuration))
	}
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(hosts []placementGRPCStream, newTable *v1pb.PlacementTables) {
	// TODO: error from disseminationOperation needs to be handle properly.
	// Otherwise, each Dapr runtime will have inconsistent hashing table.
	startedAt := time.Now()

	var wg sync.WaitGroup
	for _, host := range hosts {
		wg.Add(1)

		go func(h placementGRPCStream) {
			defer wg.Done()

			timeoutC := make(chan bool)
			stopC := make(chan bool)
			step := ""
			isTimeout := false

			go func() {
				defer close(stopC)

				timeoutC <- true
				step = "lock"
				p.disseminateOperation([]placementGRPCStream{h}, "lock", nil)
				if isTimeout {
					return
				}
				step = "update"
				p.disseminateOperation([]placementGRPCStream{h}, "update", newTable)
				if isTimeout {
					return
				}
				step = "unlock"
				p.disseminateOperation([]placementGRPCStream{h}, "unlock", nil)
			}()

			<-timeoutC

			select {
			case <-time.After(10 * time.Second):
				// timeout
				isTimeout = true
				log.Errorf("performTablesUpdate(%v) timeout 10s at step %v!!!", h, step)
				return
			case <-stopC:
				// NOTE normal
				return
			}
		}(host)
	}
	wg.Wait()

	log.Debugf("performTablesUpdate succeed %v", time.Since(startedAt))
}

func (p *Service) disseminateOperation(targets []placementGRPCStream, operation string, tables *v1pb.PlacementTables) error {
	o := &v1pb.PlacementOrder{
		Operation: operation,
		Tables:    tables,
	}
	var err error
	for _, s := range targets {
		config := retry.DefaultConfig()
		config.MaxRetries = 3
		backoff := config.NewBackOff()
		retry.NotifyRecover(
			func() error {
				// Check stream in stream pool, if stream is not available, skip to next.
				if !p.hasStreamConn(s) {
					remoteAddr := "n/a"
					if _peer, ok := peer.FromContext(s.Context()); ok {
						remoteAddr = _peer.Addr.String()
					}
					log.Debugf("runtime host (%q) is disconnected with server. go with next dissemination (operation: %s).", remoteAddr, operation)
					return nil
				}

				err = s.Send(o)
				if err != nil {
					remoteAddr := "n/a"
					if _peer, ok := peer.FromContext(s.Context()); ok {
						remoteAddr = _peer.Addr.String()
					}
					log.Errorf("error updating runtime host (%q) on %q operation: %s", remoteAddr, operation, err)
					return err
				}
				return nil
			},
			backoff,
			func(err error, d time.Duration) { log.Debugf("Attempting to disseminate again after error: %v", err) },
			func() { log.Debug("Dissemination successful.") })
	}

	return err
}
