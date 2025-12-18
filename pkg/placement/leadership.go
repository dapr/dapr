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
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/placement/monitoring"
)

// barrierWriteTimeout is the maximum duration to wait for the barrier command
// to be applied to the FSM. A barrier is used to ensure that all preceding
// operations have been committed to the FSM, guaranteeing
// that the FSM state reflects all queued writes.
const barrierWriteTimeout = 2 * time.Minute

// MonitorLeadership is used to monitor leadership changes in the Raft cluster
// so we can update the leadership status of the Placement server
func (p *Service) MonitorLeadership(ctx context.Context) error {
	var leaderLoopWg sync.WaitGroup
	var leaderCtx context.Context
	var leaderCancelFn context.CancelFunc

	raft, err := p.raftNode.Raft(ctx)
	if err != nil {
		return err
	}
	raftLeaderCh := raft.LeaderCh()

	startLeaderLoop := func() {
		if leaderCtx != nil {
			log.Error("attempted to start the leader loop while running", p.raftNode.GetID())
			return
		}

		leaderCtx, leaderCancelFn = context.WithCancel(ctx)

		leaderLoopWg.Add(1)

		go func(ctx context.Context) {
			defer leaderLoopWg.Done()
			err := p.leaderLoop(ctx)
			if err != nil {
				log.Error("placement leader loop failed", "error", err)
			}
		}(leaderCtx)
		log.Infof("raft cluster leadership acquired - %s", p.raftNode.GetID())
		return
	}

	stopLeaderLoop := func() {
		if leaderCtx == nil || leaderCancelFn == nil {
			log.Error("attempted to stop the leader loop while not running", p.raftNode.GetID())
			return
		}

		log.Debug("shutting down leader loop", p.raftNode.GetID())
		leaderCancelFn()
		leaderLoopWg.Wait()
		leaderCtx = nil
		leaderCancelFn = nil

		log.Infof("raft cluster leadership lost - %s", p.raftNode.GetID())
	}

	currentLeadershipStatus := false
	for {
		select {
		case newLeadershipStatus := <-raftLeaderCh:
			monitoring.RecordRaftPlacementLeaderStatus(newLeadershipStatus)

			if currentLeadershipStatus != newLeadershipStatus {
				// Regular leadership change (node acquired or lost leadership)
				currentLeadershipStatus = newLeadershipStatus
				if newLeadershipStatus {
					startLeaderLoop()
				} else {
					stopLeaderLoop()
				}
			} else if currentLeadershipStatus && newLeadershipStatus {
				// Leadership Flapping:
				// Server lost and then gained leadership shortly after
				//
				// The raft implementation will drop leadership events and only send the last one
				// if the receiver blocks on the leadership chanel and the leadership changes
				// multiple times during the block.
				//
				// Example:
				//  - Node acquired leadership and "true" was sent on the leadership channel (msg 1)
				//  - Client processes the leadership status change
				//  - Before the client finishes the processing, leadership changes to false (msg2)
				// 	- Leadership changes to true again (msg3)
				//	- Client finishes processing the first leadership status change
				//	- Next message received on the leadership channel is "true" (msg3).
				// During this time, this server may have received Raft transitions that
				// haven't been applied to the FSM yet.
				// We need to ensure that FSM is caught up before we start processing as a leader.
				log.Debugf("raft cluster leadership lost briefly on %s.  Waiting for FSM to catch up.", p.listenAddress)

				stopLeaderLoop()
				startLeaderLoop()
			} else {
				// The node lost leadership, regained it shortly and lost it again, or is starting up as a follower
				log.Debugf("cluster leadership gained and lost leadership immediately on %s", p.listenAddress)
			}
		case <-ctx.Done():
			if leaderCtx != nil {
				stopLeaderLoop()
			}
			return nil
		}
	}
}

func (p *Service) leaderLoop(ctx context.Context) error {
	// This loop is to ensure the FSM reflects all queued writes by applying Barrier
	// and completes leadership establishment before becoming a leader.
	for !p.hasLeadership.Load() {
		select {
		case <-ctx.Done():
			p.revokeLeadership()
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
			log.Error("failed to wait for raft barrier", "error", err)
			continue
		}

		if !p.hasLeadership.Load() {
			p.establishLeadership()
			log.Info("placement server leadership acquired", p.listenAddress)
		}
	}

	// revoke leadership process must be done before leaderLoop() ends.
	defer p.revokeLeadership()
	p.membershipChangeWorker(ctx)
	return nil
}

func (p *Service) establishLeadership() {
	p.membershipCh = make(chan hostMemberChange, membershipChangeChSize)
	p.hasLeadership.Store(true)
	monitoring.RecordPlacementLeaderStatus(true)
}

func (p *Service) revokeLeadership() {
	p.hasLeadership.Store(false)
	monitoring.RecordPlacementLeaderStatus(false)

	log.Info("Waiting until all connections are drained")
	p.streamConnGroup.Wait()
	log.Info("Connections are drained")

	p.streamConnPool.streamIndex.Store(0)
	p.disseminateLocks.Clear()
	p.memberUpdateCount.Clear()
	p.cleanupHeartbeats()
}

func (p *Service) cleanupHeartbeats() {
	p.lastHeartBeat.Range(func(key, value interface{}) bool {
		p.lastHeartBeat.Delete(key)
		return true
	})
}
