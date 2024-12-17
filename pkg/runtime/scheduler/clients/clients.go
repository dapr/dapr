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
	"time"

	"github.com/dapr/dapr/pkg/healthz"
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
	Healthz   healthz.Healthz
}

// Clients builds Scheduler clients and provides those clients in a round-robin
// fashion.
type Clients struct {
	addresses   []string
	sec         security.Handler
	clients     []schedulerv1pb.SchedulerClient
	htarget     healthz.Target
	lastUsedIdx atomic.Uint64
	running     atomic.Bool
	readyCh     chan struct{}
	closeCh     chan struct{}
}

func New(opts Options) *Clients {
	return &Clients{
		addresses: opts.Addresses,
		sec:       opts.Security,
		htarget:   opts.Healthz.AddTarget(),
		readyCh:   make(chan struct{}),
		closeCh:   make(chan struct{}),
	}
}

func (c *Clients) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("scheduler clients already running")
	}

	for {
		err := c.connectClients(ctx)
		if err == nil {
			break
		}

		log.Errorf("Failed to initialize scheduler clients: %s. Retrying...", err)

		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if len(c.clients) > 0 {
		log.Info("Scheduler clients initialized")
	}

	close(c.readyCh)
	c.htarget.Ready()
	<-ctx.Done()
	close(c.closeCh)

	return nil
}

// Next returns the next client in a round-robin manner.
func (c *Clients) Next(ctx context.Context) (schedulerv1pb.SchedulerClient, error) {
	if len(c.clients) == 0 {
		return nil, errors.New("no scheduler client initialized")
	}

	select {
	case <-c.closeCh:
		return nil, errors.New("scheduler clients closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readyCh:
	}

	//nolint:gosec
	return c.clients[int(c.lastUsedIdx.Add(1))%len(c.clients)], nil
}

// All returns all scheduler clients.
func (c *Clients) All(ctx context.Context) ([]schedulerv1pb.SchedulerClient, error) {
	select {
	case <-c.readyCh:
	case <-c.closeCh:
		return nil, errors.New("scheduler clients closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.clients, nil
}

func (c *Clients) connectClients(ctx context.Context) error {
	clients := make([]schedulerv1pb.SchedulerClient, len(c.addresses))
	for i, address := range c.addresses {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)
		client, err := client.New(ctx, address, c.sec)
		if err != nil {
			return fmt.Errorf("scheduler client not initialized for address %s: %s", address, err)
		}

		log.Infof("Scheduler client initialized for address: %s", address)
		clients[i] = client
	}

	c.clients = clients
	return nil
}
