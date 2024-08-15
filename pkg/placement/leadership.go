/*
Copyright 2024 The Dapr Authors
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
	"sync/atomic"
	"time"
)

// barrierWriteTimeout is the maximum duration to wait for the barrier command
// to be applied to the FSM. A barrier is used to ensure that all preceding
// operations have been committed to the FSM, guaranteeing
// that the FSM state reflects all queued writes.
const barrierWriteTimeout = 2 * time.Minute

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
		leaderCh          = raft.LeaderCh()
		leaderLoopRunning atomic.Bool
		loopNotRunning    = make(chan struct{})
	)
	close(loopNotRunning)

	var leaderLoopCancel context.CancelFunc
	var leaderCtx context.Context
	for {
		select {
		case <-ctx.Done():
			return nil
		case isLeader := <-leaderCh:
			if isLeader {
				if leaderLoopRunning.CompareAndSwap(false, true) {
					loopNotRunning = make(chan struct{})
					leaderCtx, leaderLoopCancel = context.WithCancel(ctx)
					p.wg.Add(1)
					go func() {
						defer func() {
							p.wg.Done()
							close(loopNotRunning)
							leaderLoopRunning.Store(false)
							// If the placement server is restarted without receiving a leadership change event,
							// the leaderLoopCancel will not be nil, so we need to cancel it here.
							if leaderLoopCancel != nil {
								leaderLoopCancel()
								leaderLoopCancel = nil
							}
						}()

						log.Info("Cluster leadership acquired")
						err := p.leaderLoop(leaderCtx)
						if err != nil {
							log.Error("placement leader loop failed", "error", err)
						}
					}()
				}
			} else {
				select {
				case <-loopNotRunning:
					log.Error("Attempted to stop leader loop when it was not running")
					continue
				default:
				}

				log.Info("Shutting down leader loop")
				if leaderLoopCancel != nil {
					leaderLoopCancel()
					leaderLoopCancel = nil
				}
				select {
				case <-loopNotRunning:
				case <-ctx.Done():
					return nil
				}
				log.Info("Cluster leadership lost")
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
	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership.Store(true)
}

func (p *Service) revokeLeadership() {
	p.hasLeadership.Store(false)

	log.Info("Waiting until all connections are drained")
	p.streamConnGroup.Wait()
	log.Info("Connections are drained")

	p.streamConnPool.streamIndex.Store(0)
	p.disseminateLocks.Clear()
	p.disseminateNextTime.Clear()
	p.memberUpdateCount.Clear()
	p.cleanupHeartbeats()
}

func (p *Service) cleanupHeartbeats() {
	p.lastHeartBeat.Range(func(key, value interface{}) bool {
		p.lastHeartBeat.Delete(key)
		return true
	})
}
