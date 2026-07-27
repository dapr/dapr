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

package clients

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.clients")

// Options contains the configuration options for the Scheduler clients.
type Options struct {
	Security security.Handler
}

// Clients builds Scheduler clients and provides those clients in a round-robin
// fashion.
type Clients struct {
	security security.Handler

	gen          *generation
	lastUsedIdx  atomic.Uint64
	currentAddrs []string

	disabled chan struct{}
	lock     sync.RWMutex
	hasInit  chan struct{}
}

func New(opts Options) *Clients {
	return &Clients{
		security: opts.Security,
		hasInit:  make(chan struct{}),
		disabled: make(chan struct{}),
	}
}

func (c *Clients) Disable() {
	close(c.disabled)
}

// Reload dials the new address set and atomically swaps it in. It never
// blocks on borrowers of the previous generation: the old connections are
// closed asynchronously, which aborts any RPCs still hung against dead
// schedulers. On dial failure the current (working) generation is preserved.
func (c *Clients) Reload(ctx context.Context, addresses []string) error {
	newGen := &generation{
		clients:  make([]schedulerv1pb.SchedulerClient, 0, len(addresses)),
		closeFns: make([]context.CancelFunc, 0, len(addresses)),
	}

	for _, address := range addresses {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)

		cl, closeFn, err := client.New(ctx, address, c.security)
		if err != nil {
			newGen.close()
			return errors.New("scheduler client not initialized for address " + address + ": " + err.Error())
		}

		log.Infof("Scheduler client initialized for address: %s", address)

		newGen.clients = append(newGen.clients, cl)
		newGen.closeFns = append(newGen.closeFns, closeFn)
	}

	if len(newGen.clients) > 0 {
		log.Info("Scheduler clients initialized")
	}

	c.lock.Lock()
	old := c.gen
	c.gen = newGen
	c.currentAddrs = addresses

	select {
	case <-c.hasInit:
	default:
		close(c.hasInit)
	}
	c.lock.Unlock()

	if old != nil {
		go old.close()
	}

	return nil
}

// Next returns the next client in a round-robin manner.
func (c *Clients) Next(ctx context.Context) (schedulerv1pb.SchedulerClient, context.CancelFunc, error) {
	select {
	case <-c.hasInit:
	case <-c.disabled:
		return nil, nil, errors.New("scheduler clients are disabled")
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	c.lock.RLock()
	defer c.lock.RUnlock()

	gen := c.gen
	if gen == nil || len(gen.clients) == 0 {
		return nil, nil, errors.New("no scheduler client initialized")
	}

	gen.wg.Add(1)
	//nolint:gosec
	return gen.clients[int(c.lastUsedIdx.Add(1))%len(gen.clients)], gen.wg.Done, nil
}

func (c *Clients) Addresses() []string {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.currentAddrs
}
