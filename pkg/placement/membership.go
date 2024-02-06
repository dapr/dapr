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
	"context"
	"errors"
	"fmt"
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
func (p *Service) MonitorLeadership(parentCtx context.Context) error {
	if p.closed.Load() {
		return errors.New("placement is closed")
	}

	ctx, cancel := context.WithCancel(parentCtx)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer cancel()
		select {
		case <-parentCtx.Done():
		case <-p.closedCh:
		}
	}()

	raft, err := p.raftNode.Raft(ctx)
	if err != nil {
		return err
	}
	var (
		leaderCh                    = raft.LeaderCh()
		oneLeaderLoopRun            sync.Once
		loopNotRunning              = make(chan struct{})
		leaderCtx, leaderLoopCancel = context.WithCancel(ctx)
	)
	close(loopNotRunning)

	defer func() {
		leaderLoopCancel()
		<-loopNotRunning
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case isLeader := <-leaderCh:
			if isLeader {
				oneLeaderLoopRun.Do(func() {
					loopNotRunning = make(chan struct{})
					p.wg.Add(1)
					go func() {
						defer p.wg.Done()
						defer close(loopNotRunning)
						log.Info("Cluster leadership acquired")
						p.leaderLoop(leaderCtx)
						oneLeaderLoopRun = sync.Once{}
					}()
				})
			} else {
				select {
				case <-loopNotRunning:
					log.Error("Attempted to stop leader loop when it was not running")
					continue
				default:
				}

				log.Info("Shutting down leader loop")
				leaderLoopCancel()
				select {
				case <-loopNotRunning:
				case <-ctx.Done():
					return nil
				}
				log.Info("Cluster leadership lost")
				leaderCtx, leaderLoopCancel = context.WithCancel(ctx)
				defer leaderLoopCancel()
			}
		}
	}
}

func (p *Service) leaderLoop(ctx context.Context) error {
	// This loop is to ensure the FSM reflects all queued writes by applying Barrier
	// and completes leadership establishment before becoming a leader.
	for !p.hasLeadership.Load() {
		// for earlier stop
		select {
		case <-ctx.Done():
			return nil
		default:
			// nop
		}

		raft, err := p.raftNode.Raft(ctx)
		if err != nil {
			return err
		}

		barrier := raft.Barrier(barrierWriteTimeout)
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

	p.membershipChangeWorker(ctx)
	return nil
}

func (p *Service) establishLeadership() {
	// Give more time to let each runtime to find the leader and connect to the leader.
	p.faultyHostDetectDuration.Store(int64(faultyHostDetectInitialDuration))

	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership.Store(true)
}

func (p *Service) revokeLeadership() {
	p.hasLeadership.Store(false)

	log.Info("Waiting until all connections are drained")
	p.streamConnGroup.Wait()
	log.Info("Connections are drained")

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
func (p *Service) membershipChangeWorker(ctx context.Context) {
	baselineHeartbeatTimestamp := p.clock.Now().UnixNano()
	log.Infof("Baseline membership heartbeat timestamp: %v", baselineHeartbeatTimestamp)

	faultyHostDetectTimer := p.clock.NewTicker(faultyHostDetectInterval)
	defer faultyHostDetectTimer.Stop()
	disseminateTimer := p.clock.NewTicker(disseminateTimerInterval)
	defer disseminateTimer.Stop()

	p.memberUpdateCount.Store(0)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.processRaftStateCommand(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case t := <-disseminateTimer.C():
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

		case t := <-faultyHostDetectTimer.C():
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
					// Before all runtimes connect to the leader of placements, it will record the leadership change
					// timestamp as the latest leadership change.
					// artursouza: The default heartbeat used to be now() but it is not correct and can lead to scenarios where a
					// runtime (pod) is terminated while there is also a leadership change in placement, meaning the host entry
					// in placement table will never have a heartbeat. With this new behavior, it will eventually expire and be
					// removed.
					heartbeat, loaded := p.lastHeartBeat.LoadOrStore(v.Name, baselineHeartbeatTimestamp)
					if !loaded {
						log.Warnf("Heartbeat not found for host: %s", v.Name)
					}

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
func (p *Service) processRaftStateCommand(ctx context.Context) {
	// logApplyConcurrency is the buffered channel to limit the concurrency
	// of raft apply command.
	logApplyConcurrency := make(chan struct{}, raftApplyCommandMaxConcurrency)

	for {
		select {
		case <-ctx.Done():
			return

		case op := <-p.membershipCh:
			switch op.cmdType {
			case raft.MemberUpsert, raft.MemberRemove:
				// MemberUpsert updates the state of dapr runtime host whenever
				// Dapr runtime sends heartbeats every X seconds.
				// MemberRemove will be queued by faultHostDetectTimer.
				// Even if ApplyCommand is failed, both commands will retry
				// until the state is consistent.
				logApplyConcurrency <- struct{}{}
				p.wg.Add(1)
				go func() {
					defer p.wg.Done()

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
							p.memberUpdateCount.Add(1)
							// disseminateNextTime will be updated whenever apply is done, so that
							// it will keep moving the time to disseminate the table, which will
							// reduce the unnecessary table dissemination.
							p.disseminateNextTime.Store(p.clock.Now().Add(disseminateTimeout).UnixNano())
						}
					}
					<-logApplyConcurrency
				}()

			case raft.TableDisseminate:
				// TableDisseminate will be triggered by disseminateTimer.
				// This disseminates the latest consistent hashing tables to Dapr runtime.
				p.performTableDissemination(ctx)
			}
		}
	}
}

func (p *Service) performTableDissemination(ctx context.Context) error {
	p.streamConnPoolLock.RLock()
	nStreamConnPool := len(p.streamConnPool)
	p.streamConnPoolLock.RUnlock()
	nTargetConns := len(p.raftNode.FSM().State().Members())

	monitoring.RecordRuntimesCount(nStreamConnPool)
	monitoring.RecordActorRuntimesCount(nTargetConns)

	// Ignore dissemination if there is no member update
	cnt := p.memberUpdateCount.Load()
	if cnt == 0 {
		return nil
	}

	p.disseminateLock.Lock()
	defer p.disseminateLock.Unlock()

	state := p.raftNode.FSM().PlacementState()

	log.Infof(
		"Start disseminating tables. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
		cnt, nStreamConnPool, nTargetConns, state.GetVersion())
	p.streamConnPoolLock.RLock()
	streamConnPool := make([]placementGRPCStream, len(p.streamConnPool))
	copy(streamConnPool, p.streamConnPool)
	p.streamConnPoolLock.RUnlock()
	if err := p.performTablesUpdate(ctx, streamConnPool, state); err != nil {
		return err
	}
	log.Infof(
		"Completed dissemination. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
		cnt, nStreamConnPool, nTargetConns, state.GetVersion())
	p.memberUpdateCount.Store(0)

	// set faultyHostDetectDuration to the default duration.
	p.faultyHostDetectDuration.Store(int64(faultyHostDetectDefaultDuration))

	return nil
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(ctx context.Context, hosts []placementGRPCStream, newTable *v1pb.PlacementTables) error {
	// TODO: error from disseminationOperation needs to be handle properly.
	// Otherwise, each Dapr runtime will have inconsistent hashing table.
	startedAt := p.clock.Now()

	// Enforce maximum API level
	if newTable != nil {
		if newTable.GetApiLevel() < p.minAPILevel {
			newTable.ApiLevel = p.minAPILevel
		}
		if p.maxAPILevel != nil && newTable.GetApiLevel() > *p.maxAPILevel {
			newTable.ApiLevel = *p.maxAPILevel
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Perform each update on all hosts in sequence
	err := p.disseminateOperationOnHosts(ctx, hosts, "lock", nil)
	if err != nil {
		return fmt.Errorf("dissemination of 'lock' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, hosts, "update", newTable)
	if err != nil {
		return fmt.Errorf("dissemination of 'update' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, hosts, "unlock", nil)
	if err != nil {
		return fmt.Errorf("dissemination of 'unlock' failed: %v", err)
	}

	log.Debugf("performTablesUpdate succeed in %v", p.clock.Since(startedAt))
	return nil
}

func (p *Service) disseminateOperationOnHosts(ctx context.Context, hosts []placementGRPCStream, operation string, tables *v1pb.PlacementTables) error {
	errCh := make(chan error)

	for i := 0; i < len(hosts); i++ {
		go func(i int) {
			errCh <- p.disseminateOperation(ctx, hosts[i], operation, tables)
		}(i)
	}

	var errs []error
	for i := 0; i < len(hosts); i++ {
		err := <-errCh
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Service) disseminateOperation(ctx context.Context, target placementGRPCStream, operation string, tables *v1pb.PlacementTables) error {
	o := &v1pb.PlacementOrder{
		Operation: operation,
		Tables:    tables,
	}

	config := retry.DefaultConfig()
	config.MaxRetries = 3
	backoff := config.NewBackOffWithContext(ctx)
	return retry.NotifyRecover(func() error {
		// Check stream in stream pool, if stream is not available, skip to next.
		if !p.hasStreamConn(target) {
			remoteAddr := "n/a"
			if p, ok := peer.FromContext(target.Context()); ok {
				remoteAddr = p.Addr.String()
			}
			log.Debugf("Runtime host %q is disconnected with server; go with next dissemination (operation: %s)", remoteAddr, operation)
			return nil
		}

		if err := target.Send(o); err != nil {
			remoteAddr := "n/a"
			if p, ok := peer.FromContext(target.Context()); ok {
				remoteAddr = p.Addr.String()
			}
			log.Errorf("Error updating runtime host %q on %q operation: %v", remoteAddr, operation, err)
			return err
		}
		return nil
	},
		backoff,
		func(err error, d time.Duration) { log.Debugf("Attempting to disseminate again after error: %v", err) },
		func() { log.Debug("Dissemination successful after failure") },
	)
}
