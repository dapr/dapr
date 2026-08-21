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

package namespaces

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/placement/internal/authorizer"
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.server.loops.namespaces")

type Options struct {
	CancelPool                  context.CancelCauseFunc
	ReplicationFactor           int64
	Authorizer                  *authorizer.Authorizer
	DisseminationTimeout        time.Duration
	DisseminationCoalesceWindow time.Duration
}

type disseminatorLoop struct {
	connections uint64
	loop        loop.Interface[loops.EventDisseminator]
}

// namespaces is the main control loop for managing stream
// connections for each namespace.
type namespaces struct {
	cancelPool                  context.CancelCauseFunc
	replicationFactor           int64
	disseminationTimeout        time.Duration
	disseminationCoalesceWindow time.Duration

	// disseminators holds the active namespace connections.
	disseminators map[string]*disseminatorLoop
	loop          loop.Interface[loops.EventNamespace]
	authorizer    *authorizer.Authorizer

	// standDown is non-nil once a stand-down began, refusing new
	// connections and running Done when the last namespace drains.
	standDown    *loops.StandDown
	drainPending int

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventNamespace] {
	ns := &namespaces{
		cancelPool:                  opts.CancelPool,
		replicationFactor:           opts.ReplicationFactor,
		disseminators:               make(map[string]*disseminatorLoop),
		authorizer:                  opts.Authorizer,
		disseminationTimeout:        opts.DisseminationTimeout,
		disseminationCoalesceWindow: opts.DisseminationCoalesceWindow,
	}

	ns.loop = loop.New[loops.EventNamespace](1024).NewLoop(ns)
	return ns.loop
}

func (n *namespaces) Handle(ctx context.Context, event loops.EventNamespace) error {
	switch e := event.(type) {
	case *loops.ConnAdd:
		return n.handleAdd(ctx, e)
	case *loops.ConnCloseStream:
		return n.handleCloseStream(e)
	case *loops.ReportedHost:
		return n.handleReportedHost(e)
	case *loops.Shutdown:
		return n.handleShutdown(e)
	case *loops.StandDown:
		return n.handleStandDown(e)
	case *loops.StandUp:
		n.standDown = nil
		n.drainPending = 0
	case *loops.DrainComplete:
		return n.handleDrainComplete(e)
	case *loops.StateTableRequest:
		n.handleStatePlacement(e)
	default:
		panic(fmt.Sprintf("unknown namespaces event type: %T", e))
	}

	return nil
}

func (n *namespaces) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	if n.standDown != nil {
		add.Cancel(n.standDown.Error)
		return nil
	}

	ns := add.InitialHost.GetNamespace()

	dissLoop, ok := n.disseminators[ns]
	if !ok {
		loop := disseminator.New(disseminator.Options{
			ReplicationFactor:           n.replicationFactor,
			NamespaceLoop:               n.loop,
			Authorizer:                  n.authorizer,
			DisseminationTimeout:        n.disseminationTimeout,
			DisseminationCoalesceWindow: n.disseminationCoalesceWindow,
			Namespace:                   ns,
		})

		n.wg.Go(func() {
			err := loop.Run(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Errorf("Error running namespaces loop: %v", err)
				n.cancelPool(err)
			}
		})

		dissLoop = &disseminatorLoop{
			loop:        loop,
			connections: 0,
		}

		n.disseminators[add.InitialHost.GetNamespace()] = dissLoop
	}

	dissLoop.connections++
	dissLoop.loop.Enqueue(add)

	return nil
}

// handleCloseStream handles a close stream request.
func (n *namespaces) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	dissLoop, ok := n.disseminators[closeStream.Namespace]
	if !ok {
		return nil
	}

	dissLoop.connections--
	dissLoop.loop.Enqueue(closeStream)

	// During a drain the disseminator's DrainComplete owns its closure.
	if dissLoop.connections == 0 && n.standDown == nil {
		delete(n.disseminators, closeStream.Namespace)
		dissLoop.loop.Close(&loops.Shutdown{
			Error: closeStream.Error,
		})
		disseminator.LoopFactory.CacheLoop(dissLoop.loop)
	}

	return nil
}

// handleReportedHost handles a reported host event.
func (n *namespaces) handleReportedHost(report *loops.ReportedHost) error {
	loop, ok := n.disseminators[report.Host.GetNamespace()]
	if !ok {
		// Ignore reported hosts for namespaces without active connections.
		return nil
	}

	loop.loop.Enqueue(report)
	return nil
}

// handleStandDown starts draining every namespace while the loop keeps
// running, so the server stays up to refuse new streams with a reason.
func (n *namespaces) handleStandDown(sd *loops.StandDown) error {
	if n.standDown != nil {
		return nil
	}
	n.standDown = sd

	n.drainPending = len(n.disseminators)
	if n.drainPending == 0 {
		sd.Done()
		return nil
	}

	for _, conn := range n.disseminators {
		conn.loop.Enqueue(&loops.Drain{Error: sd.Error})
	}

	return nil
}

// handleDrainComplete closes a drained namespace's disseminator,
// completing the stand-down when it is the last.
func (n *namespaces) handleDrainComplete(dc *loops.DrainComplete) error {
	dissLoop, ok := n.disseminators[dc.Namespace]
	if !ok {
		return nil
	}

	delete(n.disseminators, dc.Namespace)
	dissLoop.loop.Close(&loops.Shutdown{
		Error: n.standDown.Error,
	})
	disseminator.LoopFactory.CacheLoop(dissLoop.loop)

	n.drainPending--
	if n.drainPending == 0 {
		n.standDown.Done()
	}

	return nil
}

func (n *namespaces) handleShutdown(shutdown *loops.Shutdown) error {
	defer n.wg.Wait()

	for _, conn := range n.disseminators {
		conn.loop.Close(shutdown)
	}

	clear(n.disseminators)

	return nil
}
