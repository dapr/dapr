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

// membershipChangeWorker is the worker to change the state of membership
// and update the consistent hashing tables for actors.
func (p *Service) membershipChangeWorker(ctx context.Context) {
	baselineHeartbeatTimestamp := p.clock.Now().UnixNano()
	log.Infof("Baseline membership heartbeat timestamp: %v", baselineHeartbeatTimestamp)

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
			log.Debugf("faultyHostDetectCh triggered")
			defer log.Debugf("faultyHostDetectCh done")
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
						host: raft.DaprHostMember{
							Name:      h.Name,
							Namespace: h.Namespace,
							AppID:     h.AppID,
						},
					}
				}
				return true
			})
			faultyHostDetectCh = nil
		}
	}
}

// processMembershipCommands is the worker loop that
// - applies membership change commands to the raft state
// - disseminates the latest hashing table to the connected dapr runtimes
func (p *Service) processMembershipCommands(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case op := <-p.membershipCh:
			if p.membershipUpsertRemove(ctx, op) {
				log.Debugf("dissemination running for namespace %s", op.host.Namespace)
				p.membershipUpsertRemove(ctx, hostMemberChange{cmdType: raft.TableDisseminate, host: raft.DaprHostMember{Namespace: op.host.Namespace}})
			}
		}
	}
}

func (p *Service) membershipUpsertRemove(ctx context.Context, op hostMemberChange) bool {
	log.Debugf("processMembershipCommands: %v %s/%s", op.cmdType.String(), op.host.Namespace, op.host.AppID)
	switch op.cmdType {
	case raft.MemberUpsert, raft.MemberRemove:
		// MemberUpsert updates the state of dapr runtime host whenever
		// Dapr runtime sends heartbeats every X seconds.
		// MemberRemove will be queued by faultHostDetectTimer.
		// Even if ApplyCommand is failed, both commands will retry
		// until the state is consistent.
		log.Debugf("taking disseminateLockslock to schedule dissemination for namespace %s", op.host.Namespace)
		p.disseminateLocks.Lock(op.host.Namespace)
		defer p.disseminateLocks.Unlock(op.host.Namespace)

		if ctx.Err() != nil {
			return false
		}

		// ApplyCommand returns true only if the command changes the hashing table.
		updateCount, raftErr := p.raftNode.ApplyCommand(op.cmdType, op.host)
		if raftErr != nil {
			log.Errorf("fail to apply command: %v", raftErr)
			return false
		}

		numActorTypesInNamespace := p.raftNode.FSM().State().MemberCountInNamespace(op.host.Namespace)

		var lastMemberInNamespace bool
		if op.cmdType == raft.MemberRemove {
			lastMemberInNamespace = p.isLastMemberInNamespace(op)
			p.lastHeartBeat.Delete(op.host.NamespaceAndName())
			if lastMemberInNamespace {
				p.handleLastDisconnectedMemberInNamespace(op)
			}
		}

		if updateCount || lastMemberInNamespace {
			monitoring.RecordActorRuntimesCount(numActorTypesInNamespace, op.host.Namespace)
		}

		// If the change is removing the last member in the namespace,
		// we can skip the dissemination, because there are no more
		// hosts in the namespace to disseminate to
		if updateCount && !lastMemberInNamespace {
			c, _ := p.memberUpdateCount.GetOrSet(op.host.Namespace, &atomic.Uint32{})
			c.Add(1)

			cnt, ok := p.memberUpdateCount.Get(op.host.Namespace)
			if !ok {
				return false
			}
			cc := cnt.Load()
			if cc == 0 {
				return false
			}

			log.Debugf("Add raft.TableDisseminate to membershipCh. memberUpdateCountTotal count for namespace %s: %d", op.host.Namespace, cc)
			return true
		} else {
			log.Debugf("dissemination not scheduled for namespace %s", op.host.Namespace)
		}

	case raft.TableDisseminate:
		// TableDisseminate will be triggered by disseminateTimer.
		// This disseminates the latest consistent hashing tables to Dapr runtime.
		if err := p.performTableDissemination(ctx, op.host.Namespace); err != nil {
			log.Errorf("fail to perform table dissemination. Details: %v", err)
		}
	}

	return false
}

func (p *Service) isLastMemberInNamespace(op hostMemberChange) bool {
	return p.raftNode.FSM().State().MemberCountInNamespace(op.host.Namespace) == 0
}

func (p *Service) handleLastDisconnectedMemberInNamespace(op hostMemberChange) {
	log.Debugf("handling last disconnected member in namespace %s , appid %s", op.host.Namespace, op.host.AppID)
	// If this is the last host in the namespace, we should:
	// - remove namespace-specific data structures to prevent memory-leaks
	// - prevent next dissemination, because there are no more hosts in the namespace
	p.disseminateLocks.Delete(op.host.Namespace)
	p.memberUpdateCount.Del(op.host.Namespace)
}

func (p *Service) performTableDissemination(ctx context.Context, ns string) error {
	nStreamConnPool := p.streamConnPool.getStreamCount(ns)
	if nStreamConnPool == 0 {
		log.Debugf("skipping table dissemination for namespace %s, no streams", ns)
		return nil
	}

	log.Debugf("taking disseminateLockslock for table dissemination for namespace %s", ns)
	p.disseminateLocks.Lock(ns)
	defer p.disseminateLocks.Unlock(ns)

	// Ignore dissemination if there is no member update
	ac, _ := p.memberUpdateCount.GetOrSet(ns, &atomic.Uint32{})
	cnt := ac.Load()
	if cnt == 0 {
		log.Debugf("skipping table dissemination for namespace %s, no member update", ns)
		return nil
	}

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	// Get a snapshot copy of the current streams, so we don't have to
	// lock them while we're doing the dissemination (long operation)
	streams := make([]daprdStream, 0, p.streamConnPool.getStreamCount(ns))
	p.streamConnPool.forEachInNamespace(ns, func(_ uint32, stream *daprdStream) {
		streams = append(streams, *stream)
	})

	req := &tablesUpdateRequest{
		hosts: streams,
	}
	req.tables = p.raftNode.FSM().PlacementState(ns)

	numActorTypesInNamespace := p.raftNode.FSM().State().MemberCountInNamespace(ns)
	log.Infof(
		"Start disseminating tables for namespace %s. memberUpdateCount: %d, streams: %d, actor types: %d, table generation: %s",
		ns, cnt, nStreamConnPool, numActorTypesInNamespace, req.GetVersion())

	if err := p.performTablesUpdate(ctx, req); err != nil {
		return err
	}

	log.Infof(
		"Completed dissemination for namespace %s. memberUpdateCount: %d, streams: %d, actor types: %d, table generation: %s",
		ns, cnt, nStreamConnPool, numActorTypesInNamespace, req.GetVersion())
	if val, ok := p.memberUpdateCount.Get(ns); ok {
		val.Store(0)
	}

	return nil
}

// performTablesUpdate updates the connected dapr runtimes using a 3 stage commit.
// It first locks so no further dapr can be taken it. Once placement table is locked
// in runtime, it proceeds to update new table to Dapr runtimes and then unlock
// once all runtimes have been updated.
func (p *Service) performTablesUpdate(parentCtx context.Context, req *tablesUpdateRequest) error {
	startedAt := p.clock.Now()

	// Enforce maximum API level
	if req.tables != nil {
		req.SetAPILevel(p.minAPILevel, p.maxAPILevel)
	}

	ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	// Perform each update on all hosts in sequence
	err := p.disseminateOperationOnHosts(ctx, req, lockOperation)
	if err != nil {
		return fmt.Errorf("dissemination of 'lock' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, req, updateOperation)
	if err != nil {
		return fmt.Errorf("dissemination of 'update' failed: %v", err)
	}
	err = p.disseminateOperationOnHosts(ctx, req, unlockOperation)
	if err != nil {
		return fmt.Errorf("dissemination of 'unlock' failed: %v", err)
	}

	log.Debugf("performTablesUpdate succeed in %v", p.clock.Since(startedAt))
	return nil
}

func (p *Service) disseminateOperationOnHosts(ctx context.Context, req *tablesUpdateRequest, operation string) error {
	errCh := make(chan error)

	for i := range req.hosts {
		go func(i int) {
			errCh <- p.disseminateOperation(ctx, req.hosts[i], operation, req.tables)
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
	err := retry.NotifyRecover(func() error {
		// Check stream in stream pool, if stream is not available, skip to next.
		if _, ok := p.streamConnPool.getStream(target.stream); !ok {
			remoteAddr := "n/a"
			if p, ok := peer.FromContext(target.stream.Context()); ok {
				remoteAddr = p.Addr.String()
			}
			log.Debugf("Runtime host %q is disconnected from server; go with next dissemination (operation: %s)", remoteAddr, operation)
			return nil
		}

		sendCtx, sendCancelFn := context.WithTimeout(ctx, 5*time.Second) // Timeout for dissemination to a single host
		defer sendCancelFn()

		errCh := make(chan error)

		go func() {
			errCh <- target.stream.Send(o)
		}()

		select {
		case <-sendCtx.Done():
			// This code path is reached when the stream hangs or is slow to respond
			// This can happen in some environments when the sidecar is not properly shutdown, or
			// the sidecar is unresponsive, so the stream buffer fills up
			// In this case, we should not wait for the stream to respond, but instead return an error
			// and cancel the stream context

			log.Errorf("timeout sending to target %s", target.hostID)
			return fmt.Errorf("timeout sending to target %s", target.hostID)
		case err := <-errCh:
			if err != nil {
				remoteAddr := "n/a"
				if p, ok := peer.FromContext(target.stream.Context()); ok {
					remoteAddr = p.Addr.String()
				}
				log.Errorf("Error updating runtime host %q on %q operation: %v", remoteAddr, operation, err)
				return err
			}
		}
		return nil
	},
		backoff,
		func(err error, d time.Duration) { log.Debugf("Attempting to disseminate again after error: %v", err) },
		func() { log.Debug("Dissemination successful after failure") },
	)
	if err != nil {
		if target.cancelFn != nil {
			target.cancelFn()
		}
	}

	return err
}
