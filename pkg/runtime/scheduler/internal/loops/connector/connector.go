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

package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/dapr/dapr/pkg/actors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/cluster"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.loops.connector")

type Options struct {
	Namespace string
	AppID     string

	Actors   actors.Interface
	Channels *channels.Channels
	WFEngine wfengine.Interface
}

type connector struct {
	namespace string
	appID     string
	actors    actors.Interface
	channels  *channels.Channels
	wfEngine  wfengine.Interface

	currentAppRunning bool
	currentActorTypes []string
	clients           []schedulerv1pb.SchedulerClient

	closeCluster context.CancelFunc
}

func New(opts Options) loop.Interface[loops.Event] {
	return loop.New[loops.Event](1024).NewLoop(&connector{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		actors:    opts.Actors,
		channels:  opts.Channels,
		wfEngine:  opts.WFEngine,
	})
}

func (c *connector) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.Reconnect:
		c.handleReconnect(ctx, e)
	case *loops.Connect:
		c.handleConnect(ctx, e)
	case *loops.Disconnect:
		c.handleDisconnect()
	case *loops.Close:
		c.handleDisconnect()
	default:
		return fmt.Errorf("unexpected event type %T", event)
	}

	return nil
}

func (c *connector) handleConnect(ctx context.Context, e *loops.Connect) {
	c.handleDisconnect()

	c.clients = e.Clients
	c.maybeClientConnect(ctx)
}

func (c *connector) handleDisconnect() {
	c.closeClusterConnections()
	c.clients = nil
}

func (c *connector) handleReconnect(ctx context.Context, e *loops.Reconnect) {
	c.closeClusterConnections()

	if e.AppTarget != nil {
		c.currentAppRunning = *e.AppTarget
	}

	if e.ActorTypes != nil {
		c.currentActorTypes = *e.ActorTypes
	}

	c.maybeClientConnect(ctx)
}

func (c *connector) closeClusterConnections() {
	if c.closeCluster == nil {
		return
	}

	c.closeCluster()
}

func (c *connector) maybeClientConnect(ctx context.Context) {
	if len(c.clients) == 0 || (!c.currentAppRunning && len(c.currentActorTypes) == 0) {
		return
	}

	cluster := cluster.New(cluster.Options{
		Namespace: c.namespace,
		AppID:     c.appID,
		Actors:    c.actors,
		Channels:  c.channels,
		WFEngine:  c.wfEngine,

		AppTarget:  c.currentAppRunning,
		ActorTypes: c.currentActorTypes,
		Clients:    c.clients,
	})

	ctx, cancel := context.WithCancel(ctx)
	doneCh := make(chan struct{})
	go func() {
		err := cluster.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error(err, "failed to run scheduler cluster clients")
		}
		close(doneCh)
	}()

	c.closeCluster = func() {
		cancel()
		<-doneCh
		c.closeCluster = nil
	}
}
