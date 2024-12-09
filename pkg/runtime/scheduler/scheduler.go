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
	Actors    actors.Interface
	Channels  *channels.Channels
	Clients   *clients.Clients
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients   *clients.Clients
	actors    actors.Interface
	namespace string
	appID     string
	channels  *channels.Channels

	lock sync.Mutex

	appCh      chan struct{}
	appRunning bool

	running atomic.Bool
	wg      sync.WaitGroup
}

func New(opts Options) *Manager {
	return &Manager{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		actors:    opts.Actors,
		channels:  opts.Channels,
		clients:   opts.Clients,
		appCh:     make(chan struct{}),
	}
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.New("scheduler manager is already running")
	}
	defer m.wg.Wait()

	var typeUpdateCh <-chan []string = make(chan []string)
	var actorTypes []string
	if table, err := m.actors.Table(ctx); err == nil {
		typeUpdateCh, actorTypes = table.SubscribeToTypeUpdates(ctx)
	}

	for {
		m.lock.Lock()
		appRunning := m.appRunning
		appCh := m.appCh
		m.lock.Unlock()

		if !appRunning && len(actorTypes) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-appCh:
			case actorTypes = <-typeUpdateCh:
			}
		}

		m.lock.Lock()
		appCh = m.appCh
		appRunning = m.appRunning
		m.lock.Unlock()

		if !appRunning && len(actorTypes) == 0 {
			continue
		}

		lctx, cancel := context.WithCancel(ctx)

		m.wg.Add(1)
		go func() {
			select {
			case <-lctx.Done():
			case actorTypes = <-typeUpdateCh:
			case <-appCh:
			}
			cancel()
			m.wg.Done()
		}()

		err := m.loop(lctx, appRunning, actorTypes)
		cancel()
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		if err != nil {
			log.Warnf("Error watching scheduler jobs: %v", err)
		}
	}
}

func (m *Manager) loop(ctx context.Context, appTarget bool, actorTypes []string) error {
	err := m.watchJobs(ctx, appTarget, actorTypes)
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err == io.EOF {
		log.Warnf("Received EOF, re-establishing connection: %v", err)
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return nil
	}

	return fmt.Errorf("error watching scheduler jobs: %w", err)
}

// watchJobs watches for job triggers from all scheduler clients.
func (m *Manager) watchJobs(ctx context.Context, appTarget bool, actorTypes []string) error {
	req := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     m.appID,
				Namespace: m.namespace,
			},
		},
	}

	var acceptJobTypes []schedulerv1pb.JobTargetType
	if appTarget {
		acceptJobTypes = append(acceptJobTypes, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB)
	}

	if len(actorTypes) > 0 {
		acceptJobTypes = append(acceptJobTypes, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER)
		req.GetInitial().ActorTypes = actorTypes
	}

	req.GetInitial().AcceptJobTypes = acceptJobTypes

	clients, err := m.clients.All(ctx)
	if err != nil {
		return err
	}

	if len(clients) == 0 {
		log.Debug("No scheduler clients available, not watching jobs")
		<-ctx.Done()
		return ctx.Err()
	}

	runners := make([]concurrency.Runner, len(clients))

	// Accept engine to be nil, and ignore the disabled error.
	engine, _ := m.actors.Engine(ctx)
	for i := range clients {
		runners[i] = (&connector{
			req:      req,
			client:   clients[i],
			channels: m.channels,
			actors:   engine,
		}).run
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (m *Manager) StartApp() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.appRunning = true
	close(m.appCh)
	m.appCh = make(chan struct{}, 1)
}

func (m *Manager) StopApp() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.appRunning = false
	close(m.appCh)
	m.appCh = make(chan struct{}, 1)
}
