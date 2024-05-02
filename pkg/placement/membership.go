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
	"time"

	"google.golang.org/grpc/peer"

	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/concurrency"
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

	faultyHostDetectTimer := p.clock.NewTicker(faultyHostDetectInterval)
	defer faultyHostDetectTimer.Stop()
	disseminateTimer := p.clock.NewTicker(disseminateTimerInterval)
	defer disseminateTimer.Stop()

	// Reset memberUpdateCount to zero for every namespace when leadership is acquired.
	p.streamConnPool.forEachNamespace(func(ns string, _ map[uint32]*daprdStream) {
		p.memberUpdateCount.GetOrCreate(ns, 0).Store(0)
	})

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

			// check if there are actor runtime member changes per namespace.
			p.memberUpdateCount.ForEach(func(ns string, cnt *concurrency.AtomicValue[uint32]) {
				if p.disseminateNextTime.GetOrCreate(ns, 0).Load() <= t.UnixNano() && len(p.membershipCh) == 0 { //TODO: @elena - should we use getOrCreate everywhere?
					if cnt := cnt.Load(); cnt > 0 {
						log.Debugf("Add raft.TableDisseminate to membershipCh. memberUpdateCountTotal count for namespace %s: %d", ns, cnt)
						p.membershipCh <- hostMemberChange{cmdType: raft.TableDisseminate, host: raft.DaprHostMember{Namespace: ns}}
					}
				}
			})

		case t := <-faultyHostDetectTimer.C():
			// Earlier stop when leadership is lost.
			if !p.hasLeadership.Load() {
				continue
			}

			// Each dapr runtime sends the heartbeat every one second and placement will update UpdatedAt timestamp.
			// If UpdatedAt is outdated, we can mark the host as faulty node.
			// This faulty host will be removed from membership in the next dissemination period.
			if len(p.membershipCh) == 0 {
				// Loop through all members
				state := p.raftNode.FSM().State()
				state.Lock.RLock()

				for _, host := range state.AllMembers() {
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
					//TODO @elena - would it be possible for sidecars in different namespaces to have a same host.Name??
					// if yes, we might get a clash here
					heartbeat, loaded := p.lastHeartBeat.LoadOrStore(host.Name, baselineHeartbeatTimestamp)

					if !loaded {
						log.Warnf("Heartbeat not found for host: %s", host.Name)
					}

					elapsed := t.UnixNano() - heartbeat.(int64)
					if elapsed < p.faultyHostDetectDuration.Load() {
						continue
					}
					log.Debugf("Try to remove outdated host in namespace %s: %s, elapsed: %d ns", host.Namespace, host.Name, elapsed)

					p.membershipCh <- hostMemberChange{
						cmdType: raft.MemberRemove,
						host:    raft.DaprHostMember{Name: host.Name, Namespace: host.Namespace},
					}
				}

				state.Lock.RUnlock()
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
					p.disseminateLocks.Lock(op.host.Namespace)
					defer p.disseminateLocks.Unlock(op.host.Namespace)

					updated, raftErr := p.raftNode.ApplyCommand(op.cmdType, op.host)
					if raftErr != nil {
						log.Errorf("fail to apply command: %v", raftErr)
					} else {
						if op.cmdType == raft.MemberRemove {
							p.lastHeartBeat.Delete(op.host.Name)
						}

						// ApplyCommand returns true only if the command changes the hashing table.
						if updated {
							p.memberUpdateCount.GetOrCreate(op.host.Namespace, 0).Add(1)

							// disseminateNextTime will be updated whenever apply is done, so that
							// it will keep moving the time to disseminate the table, which will
							// reduce the unnecessary table dissemination.
							p.disseminateNextTime.GetOrCreate(op.host.Namespace, 0).Store(p.clock.Now().Add(disseminateTimeout).UnixNano())
						}
					}
					<-logApplyConcurrency
				}()

			case raft.TableDisseminate:
				// TableDisseminate will be triggered by disseminateTimer.
				// This disseminates the latest consistent hashing tables to Dapr runtime.
				p.performTableDissemination(ctx, op.host.Namespace)
			}
		}
	}
}

func (p *Service) performTableDissemination(ctx context.Context, ns string) error {
	nStreamConnPool := p.streamConnPool.getStreamCount(ns)
	if nStreamConnPool == 0 {
		return nil
	}

	state := p.raftNode.FSM().State()
	state.Lock.RLock()
	members, err := state.Members(ns)
	if err != nil {
		state.Lock.RUnlock()
		return err
	}
	nTargetConns := len(members)
	state.Lock.RUnlock()

	monitoring.RecordRuntimesCount(nStreamConnPool)
	monitoring.RecordActorRuntimesCount(nTargetConns)

	// Ignore dissemination if there is no member update
	cnt := p.memberUpdateCount.GetOrCreate(ns, 0).Load()
	if cnt == 0 {
		return nil
	}

	p.disseminateLocks.Lock(ns)
	defer p.disseminateLocks.Unlock(ns)

	streams := make([]daprdStream, 0, p.streamConnPool.getStreamCount(ns))
	p.streamConnPool.forEach(ns, func(_ uint32, stream *daprdStream) {
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
	p.memberUpdateCount.GetOrCreate(ns, 0).Store(0)

	// set faultyHostDetectDuration to the default duration.
	p.faultyHostDetectDuration.Store(int64(faultyHostDetectDefaultDuration))

	return nil
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(ctx context.Context, req *tablesUpdateRequest) error {
	// TODO: error from disseminationOperation needs to be handle properly.
	// Otherwise, each Dapr runtime will have inconsistent hashing table.
	startedAt := p.clock.Now()

	// Enforce maximum API level
	if req.tablesWithVNodes != nil || req.tables != nil {
		req.SetAPILevel(p.minAPILevel, p.maxAPILevel)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Perform each update on all hosts in sequence
	err := p.disseminateOperationOnHosts(ctx, req, "lock")
	if err != nil {
		return fmt.Errorf("dissemination of 'lock' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, req, "update")
	if err != nil {
		return fmt.Errorf("dissemination of 'update' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, req, "unlock")
	if err != nil {
		return fmt.Errorf("dissemination of 'unlock' failed: %v", err)
	}

	log.Debugf("performTablesUpdate succeed in %v", p.clock.Since(startedAt))
	return nil
}

func (p *Service) disseminateOperationOnHosts(ctx context.Context, req *tablesUpdateRequest, operation string) error {
	errCh := make(chan error)

	for i := 0; i < len(req.hosts); i++ {
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
	for i := 0; i < len(req.hosts); i++ {
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
		Tables:    tables,
	}

	config := retry.DefaultConfig()
	config.MaxRetries = 3
	backoff := config.NewBackOffWithContext(ctx)
	return retry.NotifyRecover(func() error {
		// Check stream in stream pool, if stream is not available, skip to next.
		if !p.streamConnPool.hasStream(target.id) {
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
