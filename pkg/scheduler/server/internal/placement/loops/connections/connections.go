/*
Copyright 2026 The Dapr Authors
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

// Package connections is the per namespace placement controller. It owns the
// namespace's streams and membership store, and disseminates per actor type
// placement tables with seq-keyed LOCK/UPDATE/UNLOCK rounds. Rounds over
// disjoint actor type sets run concurrently; changes to actor types which are
// locked by an in-flight round queue until that round completes.
package connections

import (
	"context"
	"fmt"
	"sync"
	"time"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/authorizer"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/stream"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/timeout"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.placement.connections")

type Options struct {
	Namespace          string
	NamespaceLoop      loop.Interface[loops.EventNamespace]
	Authorizer         *authorizer.Authorizer
	DisseminateTimeout time.Duration
	CoalesceWindow     time.Duration
}

// round is a single in-flight LOCK/UPDATE/UNLOCK dissemination round over a
// set of actor types.
type round struct {
	seq   uint64
	types []string

	// phase is the operation currently awaiting acks from members.
	phase schedulerv1pb.Operation

	// members are the streams participating in the round. Streams which
	// connect after the round started are not members; they receive the
	// state via their startup snapshot and subsequent rounds.
	members map[uint64]struct{}

	// started is when the round began, for the dissemination latency metric.
	started time.Time

	// acked are the members which acked the current phase.
	acked map[uint64]struct{}
}

type streamConn struct {
	loop loop.Interface[loops.EventStream]
}

type connections struct {
	namespace string
	nsLoop    loop.Interface[loops.EventNamespace]
	loop      loop.Interface[loops.EventConnections]
	authz     *authorizer.Authorizer

	disseminateTimeout time.Duration
	coalesceWindow     time.Duration

	streams   map[uint64]*streamConn
	streamIDx uint64
	store     *store.Store

	// versions are the per actor type table versions. Bumped when a round
	// including the type starts. Monotonic for the lifetime of this loop,
	// which is a single placement leadership session.
	versions map[string]uint64

	seq    uint64
	rounds map[uint64]*round

	// lockedTypes maps an actor type to the seq of the in-flight round which
	// holds it.
	lockedTypes map[string]uint64

	// pendingTypes are actor types whose table changed but which are not yet
	// covered by a round: either locked by an in-flight round, or waiting on
	// the coalesce window.
	pendingTypes map[string]struct{}

	// oneshots tracks startup snapshot pushes by seq so their acks are not
	// mistaken for round acks.
	oneshots map[uint64]uint64

	// coalesceTimer is armed after a round completes when coalesceWindow is
	// configured, batching churn into a single follow-up round.
	coalesceTimer *time.Timer

	timeoutQ *timeout.Timeout

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventConnections] {
	c := &connections{
		namespace:          opts.Namespace,
		nsLoop:             opts.NamespaceLoop,
		authz:              opts.Authorizer,
		disseminateTimeout: opts.DisseminateTimeout,
		coalesceWindow:     opts.CoalesceWindow,
		streams:            make(map[uint64]*streamConn),
		store:              store.New(),
		versions:           make(map[string]uint64),
		rounds:             make(map[uint64]*round),
		lockedTypes:        make(map[string]uint64),
		pendingTypes:       make(map[string]struct{}),
		oneshots:           make(map[uint64]uint64),
	}

	c.loop = loop.New[loops.EventConnections](1024).NewLoop(c)
	c.timeoutQ = timeout.New(timeout.Options{
		Loop:    c.loop,
		Timeout: opts.DisseminateTimeout,
	})

	return c.loop
}

func (c *connections) Handle(ctx context.Context, event loops.EventConnections) error {
	log.Debugf("Connections %s handling event: %T", c.namespace, event)

	switch e := event.(type) {
	case *loops.ConnAdd:
		c.handleAdd(ctx, e)
	case *loops.ReportedTypes:
		c.handleReportedTypes(e)
	case *loops.Ack:
		c.handleAck(e)
	case *loops.ConnCloseStream:
		c.handleCloseStream(e)
	case *loops.RoundTimeout:
		c.handleTimeout(e)
	case *loops.CoalesceFire:
		c.handleCoalesceFire()
	case *loops.Shutdown:
		c.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown connections event type: %T", e))
	}

	return nil
}

// handleAdd registers a new stream, pushes the full table snapshot to it, and
// disseminates any table changes its reported actor types cause.
func (c *connections) handleAdd(ctx context.Context, add *loops.ConnAdd) {
	streamIDx := c.streamIDx
	c.streamIDx++

	streamLoop := stream.New(ctx, stream.Options{
		IDx:           streamIDx,
		Add:           add,
		NamespaceLoop: c.nsLoop,
		Authorizer:    c.authz,
	})

	c.wg.Go(func() {
		if err := streamLoop.Run(ctx); err != nil {
			log.Errorf("Stream loop for stream %s:%d exited with error: %v", c.namespace, streamIDx, err)
		}
	})

	c.streams[streamIDx] = &streamConn{loop: streamLoop}

	changed := c.store.Set(streamIDx, add.Initial)
	c.markPending(changed)

	// Push the disseminated tables to the new stream so it can route actor
	// invocations immediately, outside of any cluster wide round. Only types
	// with no pending or in-flight change are pushed, as every other sidecar
	// holds exactly those tables: a newer table on the newcomer alone could
	// place one actor on two hosts. Pending types reach it through their
	// coming round. Types locked by an in-flight round, which fixed its
	// members before this stream joined, are marked pending so a follow-up
	// round delivers them.
	c.markPending(c.sendSnapshot(streamIDx))

	c.maybeDisseminate()
}

// handleReportedTypes handles an updated actor types report from an existing
// stream.
func (c *connections) handleReportedTypes(report *loops.ReportedTypes) {
	streamIDx := report.StreamIDx
	if _, ok := c.streams[streamIDx]; !ok {
		return
	}

	changed := c.store.Set(streamIDx, report.Host)
	c.markPending(changed)
	c.maybeDisseminate()
}

// handleCloseStream removes a stream and disseminates the table changes its
// departure causes.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) {
	affected := c.removeStream(closeStream.StreamIDx, closeStream.Error)
	c.advanceRounds(affected)
	c.maybeDisseminate()

	if len(c.streams) == 0 && c.nsLoop != nil {
		c.nsLoop.Enqueue(&loops.ConnCloseNamespace{Namespace: c.namespace})
	}
}

// removeStream tears down a stream, removes its membership, and removes it
// from every in-flight round. It returns the seqs of rounds whose phase may
// now be complete; the caller must call advanceRounds with them.
func (c *connections) removeStream(streamIDx uint64, err error) []uint64 {
	conn, ok := c.streams[streamIDx]
	if !ok {
		return nil
	}

	delete(c.streams, streamIDx)
	conn.loop.Close(&loops.StreamShutdown{Error: err})

	c.markPending(c.store.Delete(streamIDx))

	for seq, idx := range c.oneshots {
		if idx == streamIDx {
			delete(c.oneshots, seq)
		}
	}

	var affected []uint64
	for seq, r := range c.rounds {
		if _, member := r.members[streamIDx]; !member {
			continue
		}
		delete(r.members, streamIDx)
		delete(r.acked, streamIDx)
		affected = append(affected, seq)
	}

	if len(c.streams) == 0 {
		// No one left to disseminate to.
		clear(c.pendingTypes)
	}

	return affected
}

// handleShutdown tears down every stream and all state. Used on placement
// leadership loss and process shutdown; sidecars reconnect to the new leader
// and state is rebuilt from their streams.
func (c *connections) handleShutdown(shutdown *loops.Shutdown) {
	defer c.wg.Wait()

	c.stopCoalesceTimer()

	for _, conn := range c.streams {
		go conn.loop.Close(&loops.StreamShutdown{Error: shutdown.Error})
	}

	clear(c.streams)
	clear(c.rounds)
	clear(c.lockedTypes)
	clear(c.pendingTypes)
	clear(c.oneshots)
	c.store.DeleteAll()
	c.timeoutQ.Close()
}

// sendSnapshot pushes the disseminated tables to one stream and returns the
// types it skipped because an in-flight round holds them.
func (c *connections) sendSnapshot(streamIDx uint64) []string {
	conn, ok := c.streams[streamIDx]
	if !ok {
		return nil
	}

	seq := c.nextSeq()
	c.oneshots[seq] = streamIDx

	versions := make(map[string]uint64, len(c.versions))
	var types, locked []string
	for _, t := range c.store.Types() {
		if _, pending := c.pendingTypes[t]; pending {
			continue
		}
		if _, inflight := c.lockedTypes[t]; inflight {
			locked = append(locked, t)
			continue
		}
		types = append(types, t)
		versions[t] = c.versions[t]
	}

	conn.loop.Enqueue(&loops.SendSnapshot{
		Seq:      seq,
		Versions: versions,
		Tables:   c.store.Tables(types),
	})
	return locked
}

func (c *connections) nextSeq() uint64 {
	c.seq++
	return c.seq
}
