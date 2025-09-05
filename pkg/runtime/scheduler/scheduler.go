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

package scheduler

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients/wrapper"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops/connector"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops/hosts"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/watchhosts"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

type Options struct {
	Namespace          string
	AppID              string
	Actors             actors.Interface
	Channels           *channels.Channels
	WFEngine           wfengine.Interface
	Addresses          []string
	Security           security.Handler
	Healthz            healthz.Healthz
	SchedulerReminders bool
	SchedulerStreams   uint
}

// Scheduler manages the connection to the cluster of schedulers.
type Scheduler struct {
	connector  loop.Interface[loops.Event]
	hosts      loop.Interface[loops.Event]
	watchhosts *watchhosts.WatchHosts
	client     client.Interface
}

func New(opts Options) (*Scheduler, error) {
	connector := connector.New(connector.Options{
		Namespace:          opts.Namespace,
		AppID:              opts.AppID,
		Actors:             opts.Actors,
		Channels:           opts.Channels,
		WFEngine:           opts.WFEngine,
		SchedulerReminders: opts.SchedulerReminders,
	})

	if opts.SchedulerStreams < 1 {
		return nil, fmt.Errorf("must define at least 1 scheduler stream, got %d", opts.SchedulerStreams)
	}

	hosts := hosts.New(hosts.Options{
		Security:  opts.Security,
		Connector: connector,
		StreamN:   opts.SchedulerStreams,
	})

	clients := clients.New(clients.Options{
		Security: opts.Security,
	})

	watchhosts := watchhosts.New(watchhosts.Options{
		HostLoop:  hosts,
		Addresses: opts.Addresses,
		Security:  opts.Security,
		Clients:   clients,
		Healthz:   opts.Healthz,
	})

	return &Scheduler{
		connector:  connector,
		hosts:      hosts,
		watchhosts: watchhosts,
		client: wrapper.New(wrapper.Options{
			Clients: clients,
		}),
	}, nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		s.connector.Run,
		s.hosts.Run,
		s.watchhosts.Run,
		func(ctx context.Context) error {
			<-ctx.Done()
			s.hosts.Close(new(loops.Close))
			s.connector.Close(new(loops.Close))
			return ctx.Err()
		},
	).Run(ctx)
}

func (s *Scheduler) StartApp() {
	s.connector.Enqueue(&loops.Reconnect{
		AppTarget: ptr.Of(true),
	})
}

func (s *Scheduler) StopApp() {
	s.connector.Enqueue(&loops.Reconnect{
		AppTarget: ptr.Of(false),
	})
}

func (s *Scheduler) ReloadActorTypes(actorTypes []string) {
	s.connector.Enqueue(&loops.Reconnect{
		ActorTypes: ptr.Of(actorTypes),
	})
}

func (s *Scheduler) Client() client.Interface {
	return s.client
}
