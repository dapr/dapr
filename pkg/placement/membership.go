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
	"sync/atomic"
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
	GRPCContextKeyAcceptVNodes     = "dapr-accept-vnodes"
)

// membershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) membershipChangeWorker(ctx context.Context) {
	baselineHeartbeatTimestamp := p.clock.Now().UnixNano()
	log.Infof("Baseline membership heartbeat timestamp: %v", baselineHeartbeatTimestamp)

	disseminateTimer := p.clock.NewTicker(disseminateTimerInterval)
	defer disseminateTimer.Stop()

	// Reset memberUpdateCount to zero for every namespace when leadership is acquired.
	p.streamConnPool.forEachNamespace(func(ns string, _ map[uint32]*daprdStream) {
		val, _ := p.memberUpdateCount.GetOrSet(ns, &atomic.Uint32{})
		val.Store(0)
	})

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.processMembershipCommands(ctx)
	}()

	faultyHostDetectCh := time.After(faultyHostDetectDuration)

	for {
		select {
		case <-ctx.Done():
			return

		case <-faultyHostDetectCh:
			// This only runs once when the placement service acquires leadership
			// It loops through all the members in the raft store that have been connected to the
			// previous leader and checks if they have already sent a heartbeat. If they haven't,
			// they're removed from the raft store and the updated table is disseminated
			p.raftNode.FSM().State().ForEachHost(func(h *raft.DaprHostMember) bool {
				if !p.hasLeadership.Load() {
					return false
				}

				// We only care about the hosts that haven't connected to the new leader
				// The ones that have connected at some point, but have expired, will be handled
				// through the disconnect mechanism in ReportDaprStatus
				_, ok := p.lastHeartBeat.Load(h.NamespaceAndName())

				if !ok {
					log.Debugf("Try to remove outdated host: %s, no heartbeat record", h.Name)
					p.membershipCh <- hostMemberChange{
						cmdType: raft.MemberRemove,
						host:    raft.DaprHostMember{Name: h.Name, Namespace: h.Namespace},
					}
				}
				return true
			})
			faultyHostDetectCh = nil
		case t := <-disseminateTimer.C():
			// Earlier stop when leadership is lost.
			if !p.hasLeadership.Load() {
				continue
			}

			now := t.UnixNano()
			// Check if there are actor runtime member changes per namespace.
			p.disseminateNextTime.ForEach(func(ns string, disseminateTime *atomic.Int64) bool {
				if disseminateTime.Load() > now {
					return true // Continue to the next namespace
				}

				cnt, ok := p.memberUpdateCount.Get(ns)
				if !ok {
					return true
				}
				c := cnt.Load()
				if c == 0 {
					return true
				}
				if len(p.membershipCh) == 0 {
					log.Debugf("Add raft.TableDisseminate to membershipCh. memberUpdateCountTotal count for namespace %s: %d", ns, c)
					p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate, host: raft.DaprHostMember{Namespace: ns}}
				}
				return true
			})
		}
	}
}

// processMembershipCommands is the worker loop that
// - applies membership change commands to the raft state
// - disseminates the latest hashing table to the connected dapr runtimes
func (p *Service) processMembershipCommands(ctx context.Context) {
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
					p.disseminateLocks.Lock(op.host.Namespace)
					defer p.disseminateLocks.Unlock(op.host.Namespace)

					updateCount, raftErr := p.raftNode.ApplyCommand(op.cmdType, op.host)
					if raftErr != nil {
						log.Errorf("fail to apply command: %v", raftErr)
					} else {
						if op.cmdType == raft.MemberRemove {
							updateCount = p.handleDisconnectedMember(op, updateCount)
						}

						// ApplyCommand returns true only if the command changes the hashing table.
						if updateCount {
							c, _ := p.memberUpdateCount.GetOrSet(op.host.Namespace, &atomic.Uint32{})
							c.Add(1)

							// disseminateNextTime will be updated whenever apply is done, so that
							// it will keep moving the time to disseminate the table, which will
							// reduce the unnecessary table dissemination.
							val, _ := p.disseminateNextTime.GetOrSet(op.host.Namespace, &atomic.Int64{})
							val.Store(p.clock.Now().Add(p.disseminateTimeout).UnixNano())
						}
					}
					<-logApplyConcurrency
				}()

			case raft.TableDisseminate:
				// TableDisseminate will be triggered by disseminateTimer.
				// This disseminates the latest consistent hashing tables to Dapr runtime.
				if err := p.performTableDissemination(ctx, op.host.Namespace); err != nil {
					log.Errorf("fail to perform table dissemination. Details: %v", err)
				}
			}
		}
	}
}

func (p *Service) handleDisconnectedMember(op hostMemberChange, updated bool) bool {
	p.lastHeartBeat.Delete(op.host.NamespaceAndName())

	// If this is the last host in the namespace, we should:
	// - remove namespace-specific data structures to prevent memory-leaks
	// - prevent next dissemination, because there are no more hosts in the namespace
	if p.raftNode.FSM().State().MemberCountInNamespace(op.host.Namespace) == 0 {
		p.disseminateLocks.Delete(op.host.Namespace)
		p.disseminateNextTime.Del(op.host.Namespace)
		p.memberUpdateCount.Del(op.host.Namespace)
		updated = false
	}
	return updated
}

func (p *Service) performTableDissemination(ctx context.Context, ns string) error {
	nStreamConnPool := p.streamConnPool.getStreamCount(ns)
	if nStreamConnPool == 0 {
		return nil
	}

	nTargetConns := p.raftNode.FSM().State().MemberCountInNamespace(ns)

	monitoring.RecordRuntimesCount(nStreamConnPool, ns)
	monitoring.RecordActorRuntimesCount(nTargetConns, ns)

	// Ignore dissemination if there is no member update
	ac, _ := p.memberUpdateCount.GetOrSet(ns, &atomic.Uint32{})
	cnt := ac.Load()
	if cnt == 0 {
		return nil
	}

	p.disseminateLocks.Lock(ns)
	defer p.disseminateLocks.Unlock(ns)

	// Get a snapshot copy of the current streams, so we don't have to
	// lock them while we're doing the dissemination (long operation)
	streams := make([]daprdStream, 0, p.streamConnPool.getStreamCount(ns))
	p.streamConnPool.forEachInNamespace(ns, func(_ uint32, stream *daprdStream) {
		streams = append(streams, *stream)
	})

	// Check if the cluster has daprd hosts that expect vnodes;
	// older daprd versions (pre 1.13) do expect them, and newer versions (1.13+) do not.
	req := &tablesUpdateRequest{
		hosts: streams,
	}
	// Loop through all streams and check what kind of tables to disseminate (with/without vnodes)
	for _, stream := range streams {
		if stream.needsVNodes && req.tablesWithVNodes == nil {
			req.tablesWithVNodes = p.raftNode.FSM().PlacementState(true, ns)
		}
		if !stream.needsVNodes && req.tables == nil {
			req.tables = p.raftNode.FSM().PlacementState(false, ns)
		}
		if req.tablesWithVNodes != nil && req.tables != nil {
			break
		}
	}

	log.Infof(
		"Start disseminating tables for namespace %s. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
		ns, cnt, nStreamConnPool, nTargetConns, req.GetVersion())

	if err := p.performTablesUpdate(ctx, req); err != nil {
		return err
	}

	log.Infof(
		"Completed dissemination for namespace %s. memberUpdateCount: %d, streams: %d, targets: %d, table generation: %s",
		ns, cnt, nStreamConnPool, nTargetConns, req.GetVersion())
	if val, ok := p.memberUpdateCount.Get(ns); ok {
		val.Store(0)
	}

	return nil
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(ctx context.Context, req *tablesUpdateRequest) error {
	// TODO: error from disseminationOperation needs to be handled properly.
	// Otherwise, each Dapr runtime will have inconsistent hashing table.
	startedAt := p.clock.Now()

	// Enforce maximum API level
	if req.tablesWithVNodes != nil || req.tables != nil {
		req.SetAPILevel(p.minAPILevel, p.maxAPILevel)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	errCh = make(chan error)

	go func() {
		err := p.disseminateOperationOnHosts(ctx, req, lockOperation)
		if err != nil {
			errCh <- fmt.Errorf("dissemination of 'lock' failed: %v", err)
		}
		err = p.disseminateOperationOnHosts(ctx, req, updateOperation)
		if err != nil {
			errCh <- fmt.Errorf("dissemination of 'update' failed: %v", err)
		}
		err = p.disseminateOperationOnHosts(ctx, req, unlockOperation)
		if err != nil {
			errCh <- fmt.Errorf("dissemination of 'unlock' failed: %v", err)
		}
		errCh <- nil;
	}();

	select {
	case err := <- errCh:
		if err != nil {
			return err
		}
	case <- ctx.Done():
		return fmt.Errorf("performTablesUpdate failed: %v", ctx.Err())
	}

	log.Debugf("performTablesUpdate succeed in %v", p.clock.Since(startedAt))
	return nil
}

func (p *Service) disseminateOperationOnHosts(ctx context.Context, req *tablesUpdateRequest, operation string) error {
	errCh := make(chan error)

	for i := range req.hosts {
		go func(i int) {
			var tableToSend *v1pb.PlacementTables
			if req.hosts[i].needsVNodes && req.tablesWithVNodes != nil {
				tableToSend = req.tablesWithVNodes
			} else if !req.hosts[i].needsVNodes && req.tables != nil {
				tableToSend = req.tables
			}

			errCh <- p.disseminateOperation(ctx, req.hosts[i], operation, tableToSend)
		}(i)
	}

	var errs []error
	for range req.hosts {
		err := <-errCh
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Service) disseminateOperation(ctx context.Context, target daprdStream, operation string, tables *v1pb.PlacementTables) error {
	o := &v1pb.PlacementOrder{
		Operation: operation,
	}
	if operation == updateOperation {
		o.Tables = tables
	}

	config := retry.DefaultConfig()
	config.MaxRetries = 3
	backoff := config.NewBackOffWithContext(ctx)
	return retry.NotifyRecover(func() error {
		// Check stream in stream pool, if stream is not available, skip to next.
		if _, ok := p.streamConnPool.getStream(target.stream); !ok {
			remoteAddr := "n/a"
			if p, ok := peer.FromContext(target.stream.Context()); ok {
				remoteAddr = p.Addr.String()
			}
			log.Debugf("Runtime host %q is disconnected from server; go with next dissemination (operation: %s)", remoteAddr, operation)
			return nil
		}

		if err := target.stream.Send(o); err != nil {
			remoteAddr := "n/a"
			if p, ok := peer.FromContext(target.stream.Context()); ok {
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
