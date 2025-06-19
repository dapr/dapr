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

package handler

import (
	"context"
	"strings"
	"sync"

	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/cluster"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
)

type Options struct {
	Security  security.Handler
	Cluster   *cluster.Cluster
	Healthz   healthz.Healthz
	Addresses []string
}

type Handler struct {
	security  security.Handler
	clients   *clients.Clients
	cluster   *cluster.Cluster
	htarget   healthz.Target
	addresses []string

	wrapper *wrapper

	lock    sync.RWMutex
	readyCh chan struct{}

	broadcastAddresses []string
}

func New(opts Options) *Handler {
	h := &Handler{
		security:  opts.Security,
		readyCh:   make(chan struct{}),
		cluster:   opts.Cluster,
		htarget:   opts.Healthz.AddTarget("scheduler-handler"),
		addresses: opts.Addresses,
	}

	if h.NotEnabled() {
		h.htarget.Ready()
	}

	h.wrapper = &wrapper{h}
	return h
}

func (h *Handler) Client() client.Client {
	return h.wrapper
}

func (h *Handler) ConnectClients(ctx context.Context, addresses []string) error {
	h.lock.Lock()

	var err error
	h.clients, err = clients.New(ctx, clients.Options{
		Addresses: addresses,
		Security:  h.security,
	})
	if err != nil {
		h.lock.Unlock()
		return err
	}

	h.broadcastAddresses = addresses
	readyCh := h.readyCh
	h.lock.Unlock()

	err = concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			return h.cluster.RunClients(ctx, h.clients)
		},
		func(ctx context.Context) error {
			if err = h.cluster.WaitForReady(ctx); err != nil {
				return err
			}
			close(readyCh)
			h.htarget.Ready()

			<-ctx.Done()
			return ctx.Err()
		},
	).Run(ctx)

	h.lock.Lock()
	h.readyCh = make(chan struct{})
	h.htarget.NotReady()
	h.clients.Close()
	h.broadcastAddresses = nil
	h.lock.Unlock()

	return err
}

func (h *Handler) next(ctx context.Context) (v1pb.SchedulerClient, error) {
	for {
		h.lock.RLock()
		readyCh := h.readyCh
		h.lock.RUnlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-readyCh:
		}

		// Ensure still ready as there is a race from above.
		h.lock.RLock()
		select {
		case <-h.readyCh:
			defer h.lock.RUnlock()
			return h.clients.Next()
		default:
			h.lock.RUnlock()
		}
	}
}

func (h *Handler) NotEnabled() bool {
	return len(h.addresses) == 0 ||
		(len(h.addresses) == 1 &&
			strings.TrimSpace(strings.Trim(h.addresses[0], `"'`)) == "")
}
