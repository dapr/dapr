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
	"sync/atomic"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.clients")

// Options contains the configuration options for the Scheduler clients.
type Options struct {
	Addresses []string
	Security  security.Handler
}

// Clients builds Scheduler clients and provides those clients in a round-robin
// fashion.
type Clients struct {
	clients     []schedulerv1pb.SchedulerClient
	closeFns    []context.CancelFunc
	lastUsedIdx atomic.Uint64
}

func New(ctx context.Context, opts Options) (*Clients, error) {
	c := &Clients{
		clients:  make([]schedulerv1pb.SchedulerClient, 0, len(opts.Addresses)),
		closeFns: make([]context.CancelFunc, 0, len(opts.Addresses)),
	}

	for _, address := range opts.Addresses {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)
		client, closeFn, err := client.New(ctx, address, opts.Security)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("scheduler client not initialized for address %s: %s", address, err)
		}

		log.Infof("Scheduler client initialized for address: %s", address)
		c.clients = append(c.clients, client)
		c.closeFns = append(c.closeFns, closeFn)
	}

	if len(c.clients) > 0 {
		log.Info("Scheduler clients initialized")
	}

	return c, nil
}

// Next returns the next client in a round-robin manner.
func (c *Clients) Next() (schedulerv1pb.SchedulerClient, error) {
	if len(c.clients) == 0 {
		return nil, errors.New("no scheduler client initialized")
	}

	//nolint:gosec
	return c.clients[int(c.lastUsedIdx.Add(1))%len(c.clients)], nil
}

// All returns all scheduler clients.
func (c *Clients) All() []schedulerv1pb.SchedulerClient {
	return c.clients
}

func (c *Clients) Close() {
	for _, closeFn := range c.closeFns {
		closeFn()
	}
}
