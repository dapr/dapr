/*
Copyright 2025 The Dapr Authors
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

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.namespaces")

type Options struct {
	Cron       api.Interface
	CancelPool context.CancelCauseFunc
}

type connectionLoop struct {
	connections uint64
	loop        loop.Interface[loops.Event]
}

// namespaces is the main control loop for managing stream
// connections for each namespace.
type namespaces struct {
	cron       api.Interface
	cancelPool context.CancelCauseFunc

	// connections holds the active namespace connections.
	connections map[string]*connectionLoop
	loop        loop.Interface[loops.Event]

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.Event] {
	ns := &namespaces{
		cron:        opts.Cron,
		cancelPool:  opts.CancelPool,
		connections: make(map[string]*connectionLoop),
	}

	ns.loop = loop.New[loops.Event](1024).NewLoop(ns)
	return ns.loop
}

func (n *namespaces) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.ConnAdd:
		return n.handleAdd(ctx, e)
	case *loops.ConnCloseStream:
		return n.handleCloseStream(e)
	case *loops.TriggerRequest:
		return n.handleTriggerRequest(e)
	case *loops.Shutdown:
		return n.handleShutdown(e)
	default:
		return fmt.Errorf("unknown connections event type: %T", e)
	}
}

// handleAdd adds a connection to the pool for a given namespace/appID.
func (n *namespaces) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	connLoop, ok := n.connections[add.Request.GetNamespace()]
	if !ok {
		loop := connections.New(connections.Options{
			Cron:          n.cron,
			NamespaceLoop: n.loop,
		})

		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			err := loop.Run(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Errorf("Error running stream loop: %v", err)
				n.cancelPool(err)
			}
		}()

		connLoop = &connectionLoop{
			loop:        loop,
			connections: 0,
		}

		n.connections[add.Request.GetNamespace()] = connLoop
	}

	connLoop.connections++
	connLoop.loop.Enqueue(add)

	return nil
}

// handleCloseStream handles a close stream request.
func (n *namespaces) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	connLoop, ok := n.connections[closeStream.Namespace]
	if !ok {
		return nil
	}

	connLoop.connections--
	connLoop.loop.Enqueue(closeStream)

	if connLoop.connections == 0 {
		delete(n.connections, closeStream.Namespace)
		connLoop.loop.Close(new(loops.Shutdown))
	}

	return nil
}

// handleTriggerRequest handles a trigger request for a job.
func (n *namespaces) handleTriggerRequest(req *loops.TriggerRequest) error {
	loop, ok := n.connections[req.Job.GetMetadata().GetNamespace()]
	if !ok {
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return nil
	}

	loop.loop.Enqueue(req)
	return nil
}

// handleShutdown handles the shutdown of the connections.
func (n *namespaces) handleShutdown(shutdown *loops.Shutdown) error {
	defer n.wg.Wait()

	for _, conn := range n.connections {
		conn.loop.Close(shutdown)
	}

	clear(n.connections)

	return nil
}
