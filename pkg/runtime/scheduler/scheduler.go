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
	"time"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/cluster"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/handler"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/watchhosts"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

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
}

// Scheduler manages the connection to the cluster of schedulers.
type Scheduler struct {
	handler   *handler.Handler
	security  security.Handler
	cluster   *cluster.Cluster
	addresses []string
}

func New(opts Options) *Scheduler {
	cluster := cluster.New(cluster.Options{
		Namespace: opts.Namespace,
		AppID:     opts.AppID,
		Actors:    opts.Actors,
		Channels:  opts.Channels,
		WFEngine:  opts.WFEngine,

		SchedulerReminders: opts.SchedulerReminders,
	})

	return &Scheduler{
		addresses: opts.Addresses,
		security:  opts.Security,
		cluster:   cluster,
		handler: handler.New(handler.Options{
			Security:  opts.Security,
			Healthz:   opts.Healthz,
			Cluster:   cluster,
			Addresses: opts.Addresses,
		}),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s.handler.NotEnabled() {
		log.Warn("Scheduler disabled, not connecting...")
		<-ctx.Done()
		return nil
	}

	for {
		watchHosts := watchhosts.New(watchhosts.Options{
			Addresses: s.addresses,
			Security:  s.security,
		})

		err := concurrency.NewRunnerManager(
			watchHosts.Run,
			func(ctx context.Context) error {
				addrsCh := watchHosts.Addresses(ctx)
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case actx := <-addrsCh:
						if err := s.handler.ConnectClients(actx, actx.Addresses); err != nil {
							return err
						}
						if ctx.Err() != nil {
							return ctx.Err()
						}

						log.Infof("Attempting to reconnect to schedulers")
					}
				}
			},
		).Run(ctx)
		if ctx.Err() != nil {
			return err
		}

		if err != nil {
			log.Errorf("Error connecting to Schedulers, reconnecting: %s", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (s *Scheduler) Client() client.Client {
	return s.handler.Client()
}

func (s *Scheduler) StartApp(ctx context.Context) {
	s.cluster.StartApp()
}

func (s *Scheduler) StopApp(ctx context.Context) {
	s.cluster.StopApp()
}
