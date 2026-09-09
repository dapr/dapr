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

// Package namespaces is the top level placement controller, scoping streams
// and placement state by namespace. A per namespace connections loop is
// created lazily on the first stream of a namespace and torn down when its
// last stream closes.
package namespaces

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/authorizer"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/connections"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.placement.namespaces")

type Options struct {
	Authorizer         *authorizer.Authorizer
	DisseminateTimeout time.Duration
	CoalesceWindow     time.Duration
}

type connectionsLoop struct {
	connections uint64
	loop        loop.Interface[loops.EventConnections]
}

type namespaces struct {
	authorizer         *authorizer.Authorizer
	disseminateTimeout time.Duration
	coalesceWindow     time.Duration

	// accepting is whether this scheduler currently accepts placement
	// streams, i.e. whether it is the placement leader.
	accepting bool

	namespaces map[string]*connectionsLoop
	loop       loop.Interface[loops.EventNamespace]

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventNamespace] {
	n := &namespaces{
		authorizer:         opts.Authorizer,
		disseminateTimeout: opts.DisseminateTimeout,
		coalesceWindow:     opts.CoalesceWindow,
		namespaces:         make(map[string]*connectionsLoop),
	}

	n.loop = loop.New[loops.EventNamespace](1024).NewLoop(n)
	return n.loop
}

func (n *namespaces) Handle(ctx context.Context, event loops.EventNamespace) error {
	switch e := event.(type) {
	case *loops.ConnAdd:
		n.handleAdd(ctx, e)
	case *loops.SetLeader:
		n.handleSetLeader(e)
	case *loops.ConnCloseStream:
		n.handleCloseStream(e)
	case *loops.ConnCloseNamespace:
		n.handleCloseNamespace(e)
	case *loops.ReportedTypes:
		n.route(e.Host.GetNamespace(), e)
	case *loops.Ack:
		n.route(e.Namespace, e)
	case *loops.Shutdown:
		n.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown namespaces event type: %T", e))
	}

	return nil
}

func (n *namespaces) handleAdd(ctx context.Context, add *loops.ConnAdd) {
	if !n.accepting {
		add.Cancel(status.Error(codes.FailedPrecondition, "scheduler is not the placement leader"))
		return
	}

	ns := add.Initial.GetNamespace()

	connsLoop, ok := n.namespaces[ns]
	if !ok {
		cl := connections.New(connections.Options{
			Namespace:          ns,
			NamespaceLoop:      n.loop,
			Authorizer:         n.authorizer,
			DisseminateTimeout: n.disseminateTimeout,
			CoalesceWindow:     n.coalesceWindow,
		})

		n.wg.Go(func() {
			if err := cl.Run(ctx); err != nil && ctx.Err() == nil {
				log.Errorf("Error running placement connections loop for namespace %s: %v", ns, err)
			}
		})

		connsLoop = &connectionsLoop{loop: cl}
		n.namespaces[ns] = connsLoop
	}

	connsLoop.connections++
	connsLoop.loop.Enqueue(add)
}

func (n *namespaces) handleSetLeader(e *loops.SetLeader) {
	if e.Leader == n.accepting {
		return
	}

	n.accepting = e.Leader
	if e.Leader {
		return
	}

	// Lost placement leadership: close every stream. Sidecars reconnect to
	// the new leader, which rebuilds state from their reports.
	n.closeAll(status.Error(codes.FailedPrecondition, "scheduler lost placement leadership"))
}

func (n *namespaces) handleCloseStream(closeStream *loops.ConnCloseStream) {
	connsLoop, ok := n.namespaces[closeStream.Namespace]
	if !ok {
		return
	}

	if connsLoop.connections > 0 {
		connsLoop.connections--
	}
	connsLoop.loop.Enqueue(closeStream)
}

// handleCloseNamespace tears down a namespace once its connections loop has
// confirmed its last stream is gone and no add has arrived since.
func (n *namespaces) handleCloseNamespace(closeNS *loops.ConnCloseNamespace) {
	connsLoop, ok := n.namespaces[closeNS.Namespace]
	if !ok || connsLoop.connections != 0 {
		return
	}

	delete(n.namespaces, closeNS.Namespace)
	connsLoop.loop.Close(new(loops.Shutdown))
}

func (n *namespaces) route(ns string, event loops.EventConnections) {
	connsLoop, ok := n.namespaces[ns]
	if !ok {
		return
	}
	connsLoop.loop.Enqueue(event)
}

func (n *namespaces) handleShutdown(shutdown *loops.Shutdown) {
	n.accepting = false
	err := shutdown.Error
	if err == nil {
		err = status.Error(codes.Unavailable, "placement is shutting down")
	}
	n.closeAll(err)
	n.wg.Wait()
}

func (n *namespaces) closeAll(err error) {
	for _, connsLoop := range n.namespaces {
		connsLoop.loop.Close(&loops.Shutdown{Error: err})
	}
	clear(n.namespaces)
}
