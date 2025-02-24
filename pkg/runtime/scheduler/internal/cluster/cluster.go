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

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.cluster")

type Options struct {
	Namespace string
	AppID     string
	Actors    actors.Interface
	Channels  *channels.Channels
	WFEngine  wfengine.Interface

	SchedulerReminders bool
}

// Cluster manages connections to multiple schedulers.
type Cluster struct {
	actors    actors.Interface
	namespace string
	appID     string
	channels  *channels.Channels
	wfengine  wfengine.Interface

	schedulerReminders bool

	lock sync.Mutex

	appCh      chan struct{}
	appRunning bool
	readyCh    chan struct{}

	wg sync.WaitGroup
}

func New(opts Options) *Cluster {
	return &Cluster{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		actors:    opts.Actors,
		wfengine:  opts.WFEngine,
		channels:  opts.Channels,
		appCh:     make(chan struct{}),
		readyCh:   make(chan struct{}),

		schedulerReminders: opts.SchedulerReminders,
	}
}

// Run starts watching for job triggers from all scheduler clients.
func (c *Cluster) RunClients(ctx context.Context, clients *clients.Clients) error {
	defer c.wg.Wait()

	var typeUpdateCh <-chan []string = make(chan []string)
	var actorTypes []string

	// Only subscribe to actor types to watch if we are using Scheduler actor
	// reminders.
	if c.schedulerReminders {
		if table, err := c.actors.Table(ctx); err == nil {
			typeUpdateCh, actorTypes = table.SubscribeToTypeUpdates(ctx)
		}
	}

	for {
		c.lock.Lock()
		appRunning := c.appRunning
		appCh := c.appCh
		c.lock.Unlock()

		if !appRunning && len(actorTypes) == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-appCh:
			case actorTypes = <-typeUpdateCh:
			}
		}

		c.lock.Lock()
		appCh = c.appCh
		appRunning = c.appRunning
		c.lock.Unlock()

		if !appRunning && len(actorTypes) == 0 {
			continue
		}

		lctx, cancel := context.WithCancel(ctx)

		c.wg.Add(1)
		go func() {
			select {
			case <-lctx.Done():
			case actorTypes = <-typeUpdateCh:
			case <-appCh:
			}
			cancel()
			c.wg.Done()
		}()

		err := c.loop(lctx, clients, appRunning, actorTypes)
		cancel()
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}

		if err != nil {
			log.Warnf("Error watching scheduler jobs: %v", err)
		}
	}
}

func (c *Cluster) loop(ctx context.Context, clients *clients.Clients, appTarget bool, actorTypes []string) error {
	err := c.watchJobs(ctx, clients, appTarget, actorTypes)
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
func (c *Cluster) watchJobs(ctx context.Context, clients *clients.Clients, appTarget bool, actorTypes []string) error {
	req := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     c.appID,
				Namespace: c.namespace,
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

	cls := clients.All()

	if len(cls) == 0 {
		log.Debug("No scheduler clients available, not watching jobs")
		<-ctx.Done()
		return ctx.Err()
	}

	runners := make([]concurrency.Runner, len(cls)+1)
	connectors := make([]*connector, len(cls))

	// Accept engine to be nil, and ignore the disabled error.
	engine, _ := c.actors.Engine(ctx)
	for i := range cls {
		connectors[i] = &connector{
			req:      req,
			client:   cls[i],
			channels: c.channels,
			actors:   engine,
			wfengine: c.wfengine,
			readyCh:  make(chan struct{}),
		}
		runners[i] = connectors[i].run
	}

	runners[len(cls)] = func(ctx context.Context) error {
		for _, conn := range connectors {
			select {
			case <-conn.readyCh:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		c.lock.Lock()
		close(c.readyCh)
		c.lock.Unlock()

		<-ctx.Done()

		c.lock.Lock()
		c.readyCh = make(chan struct{})
		c.lock.Unlock()
		return ctx.Err()
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (c *Cluster) StartApp() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.appRunning = true
	close(c.appCh)
	c.appCh = make(chan struct{}, 1)
}

func (c *Cluster) StopApp() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.appRunning = false
	close(c.appCh)
	c.appCh = make(chan struct{}, 1)
}

func (c *Cluster) WaitForReady(ctx context.Context) error {
	c.lock.Lock()
	readyCh := c.readyCh
	c.lock.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-readyCh:
		return nil
	}
}
