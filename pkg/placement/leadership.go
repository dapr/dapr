package placement

import (
	"context"
	"errors"
	"sync"
	"time"
)

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

	p.streamConnPool.streamIndexCnt.Store(0)
	p.cleanupHeartbeats()
}

func (p *Service) cleanupHeartbeats() {
	p.lastHeartBeat.Range(func(key, value interface{}) bool {
		p.lastHeartBeat.Delete(key)
		return true
	})
}
