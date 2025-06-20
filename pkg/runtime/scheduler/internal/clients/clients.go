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
	"fmt"
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

	clients     []schedulerv1pb.SchedulerClient
	closeFns    []context.CancelFunc
	lastUsedIdx atomic.Uint64

	disabled     chan struct{}
	currentAddrs []string

	lock    sync.RWMutex
	hasInit chan struct{}
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

func (c *Clients) Reload(ctx context.Context, addresses []string) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.currentAddrs = []string{}
	c.close()

	c.clients = make([]schedulerv1pb.SchedulerClient, 0, len(addresses))
	c.closeFns = make([]context.CancelFunc, 0, len(addresses))

	for _, address := range addresses {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)
		client, closeFn, err := client.New(ctx, address, c.security)
		if err != nil {
			c.close()
			return fmt.Errorf("scheduler client not initialized for address %s: %s", address, err)
		}

		log.Infof("Scheduler client initialized for address: %s", address)
		c.clients = append(c.clients, client)
		c.closeFns = append(c.closeFns, closeFn)
	}

	if len(c.clients) > 0 {
		log.Info("Scheduler clients initialized")
	}

	c.currentAddrs = addresses

	select {
	case <-c.hasInit:
	default:
		close(c.hasInit)
	}

	return nil
}

// Next returns the next client in a round-robin manner.
func (c *Clients) Next(ctx context.Context) (schedulerv1pb.SchedulerClient, error) {
	select {
	case <-c.hasInit:
	case <-c.disabled:
		return nil, errors.New("scheduler clients are disabled")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.lock.RLock()
	defer c.lock.RUnlock()

	if len(c.clients) == 0 {
		return nil, errors.New("no scheduler client initialized")
	}

	//nolint:gosec
	return c.clients[int(c.lastUsedIdx.Add(1))%len(c.clients)], nil
}

func (c *Clients) Addresses() []string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.currentAddrs
}

func (c *Clients) close() {
	var wg sync.WaitGroup
	wg.Add(len(c.closeFns))
	for _, closeFn := range c.closeFns {
		go func() {
			closeFn()
			wg.Done()
		}()
	}
	wg.Wait()
}
