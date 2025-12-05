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

package hosts

import (
	"context"
	"fmt"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	Security  security.Handler
	Connector loop.Interface[loops.Event]
	StreamN   uint
}

type hosts struct {
	security  security.Handler
	streamN   uint
	connector loop.Interface[loops.Event]

	closeConns []context.CancelFunc
}

func New(opts Options) loop.Interface[loops.Event] {
	return loop.New[loops.Event](1024).NewLoop(&hosts{
		streamN:   opts.StreamN,
		security:  opts.Security,
		connector: opts.Connector,
	})
}

func (h *hosts) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.ReloadClients:
		return h.handleReloadClients(ctx, e)
	case *loops.Close:
		h.handleCloseCons()
		return nil
	default:
		return fmt.Errorf("unexpected event type %T", e)
	}
}

func (h *hosts) handleReloadClients(ctx context.Context, event *loops.ReloadClients) error {
	h.handleCloseCons()

	var clients []schedulerv1pb.SchedulerClient
	var closeConns []context.CancelFunc

	for range h.streamN {
		for _, addr := range event.Addresses {
			client, closeCon, err := client.New(ctx, addr, h.security)
			if err != nil {
				return fmt.Errorf("failed to create scheduler client for address %s: %w", addr, err)
			}

			clients = append(clients, client)
			closeConns = append(closeConns, closeCon)
		}
	}

	h.closeConns = closeConns
	h.connector.Enqueue(&loops.Connect{Clients: clients})

	return nil
}

func (h *hosts) handleCloseCons() {
	for _, closeCon := range h.closeConns {
		closeCon()
	}
	h.closeConns = nil
}
