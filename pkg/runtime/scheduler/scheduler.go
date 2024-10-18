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
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/actors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

type Options struct {
	Namespace string
	AppID     string
	Channels  *channels.Channels
	Clients   *clients.Clients
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients   *clients.Clients
	actors    actors.ActorRuntime
	namespace string
	appID     string
	channels  *channels.Channels

	stopStartCh chan struct{}
	lock        sync.Mutex
	started     bool
	stopped     bool
	running     atomic.Bool
	wg          sync.WaitGroup
}

func New(opts Options) *Manager {
	return &Manager{
		namespace:   opts.Namespace,
		appID:       opts.AppID,
		channels:    opts.Channels,
		clients:     opts.Clients,
		stopStartCh: make(chan struct{}, 1),
	}
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.New("scheduler manager is already running")
	}

	for {
		if err := m.watchJobs(ctx); err != nil {
			// don't retry if closing down
			if ctx.Err() != nil {
				return nil //nolint:nilerr
			}
			if err == io.EOF {
				log.Warnf("Received EOF, re-establishing connection: %v", err)
				continue
			}
			return fmt.Errorf("error watching scheduler jobs: %w", err)
		}

		// don't retry if closing down
		if ctx.Err() != nil {
			return nil //nolint:nilerr
		}
	}
}

// watchJobs watches for job triggers from all scheduler clients.
func (m *Manager) watchJobs(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stopStartCh:
	}

	var entities []string
	if m.actors != nil {
		entities = m.actors.Entities()
	}

	req := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     m.appID,
				Namespace: m.namespace,
				ActorTypes: append(entities,
					fmt.Sprintf("dapr.internal.%s.%s.workflow", m.namespace, m.appID),
					fmt.Sprintf("dapr.internal.%s.%s.activity", m.namespace, m.appID),
				),
			},
		},
	}

	clients, err := m.clients.All(ctx)
	if err != nil {
		return err
	}

	runners := make([]concurrency.Runner, len(clients)+1)

	for i := range clients {
		runners[i] = (&connector{
			req:      req,
			client:   clients[i],
			channels: m.channels,
			actors:   m.actors,
		}).run
	}

	runners[len(clients)] = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopStartCh:
			return nil
		}
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

// Start starts the scheduler manager with the given actors runtime, if it is
// enabled, to begin receiving job triggers.
func (m *Manager) Start(actors actors.ActorRuntime) {
	m.lock.Lock()
	defer m.lock.Unlock()
	if !m.started {
		m.started = true
		m.stopped = false
		m.actors = actors
		close(m.stopStartCh)
		m.stopStartCh = make(chan struct{}, 1)
	}
}

// Stop stops the scheduler manager, which stops watching for job triggers.
func (m *Manager) Stop() {
	m.lock.Lock()
	defer m.lock.Unlock()
	if m.started && !m.stopped {
		m.stopped = true
		m.started = false
		m.stopStartCh <- struct{}{}
		m.wg.Wait()
	}
}
