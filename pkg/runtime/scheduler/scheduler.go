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
	"strings"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/cluster"
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
	addresses []string
	security  security.Handler
	htarget   healthz.Target

	cluster *cluster.Cluster
	clients *clients.Clients

	broadcastAddresses []string

	lock     sync.RWMutex
	readyCh  chan struct{}
	disabled chan struct{}
}

func New(opts Options) *Scheduler {
	return &Scheduler{
		addresses: opts.Addresses,
		security:  opts.Security,
		cluster: cluster.New(cluster.Options{
			Namespace: opts.Namespace,
			AppID:     opts.AppID,
			Actors:    opts.Actors,
			Channels:  opts.Channels,
			WFEngine:  opts.WFEngine,

			SchedulerReminders: opts.SchedulerReminders,
		}),
		broadcastAddresses: opts.Addresses,
		htarget:            opts.Healthz.AddTarget(),
		readyCh:            make(chan struct{}),
		disabled:           make(chan struct{}),
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if len(s.addresses) == 0 ||
		(len(s.addresses) == 1 && strings.TrimSpace(strings.Trim(s.addresses[0], `"'`)) == "") {
		s.htarget.Ready()
		log.Warn("Scheduler disabled, not connecting...")
		close(s.disabled)
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
						if err := s.connectClients(actx, actx.Addresses); err != nil {
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

func (s *Scheduler) connectClients(ctx context.Context, addresses []string) error {
	s.lock.Lock()

	var err error
	s.clients, err = clients.New(ctx, clients.Options{
		Addresses: addresses,
		Security:  s.security,
	})
	if err != nil {
		s.lock.Unlock()
		return err
	}

	s.broadcastAddresses = addresses
	readyCh := s.readyCh
	s.lock.Unlock()

	err = concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			return s.cluster.RunClients(ctx, s.clients)
		},
		func(ctx context.Context) error {
			if err = s.cluster.WaitForReady(ctx); err != nil {
				return err
			}
			close(readyCh)
			s.htarget.Ready()

			<-ctx.Done()
			return ctx.Err()
		},
	).Run(ctx)

	s.lock.Lock()
	s.readyCh = make(chan struct{})
	s.htarget.NotReady()
	s.clients.Close()
	s.broadcastAddresses = nil
	s.lock.Unlock()

	return err
}

func (s *Scheduler) Next(ctx context.Context) (schedulerv1pb.SchedulerClient, error) {
	var client schedulerv1pb.SchedulerClient
	if err := s.callWhenReady(ctx, func(ctx context.Context) error {
		var err error
		client, err = s.clients.Next()
		return err
	}); err != nil {
		return nil, err
	}
	return client, nil
}

func (s *Scheduler) All(ctx context.Context) ([]schedulerv1pb.SchedulerClient, error) {
	var clients []schedulerv1pb.SchedulerClient
	if err := s.callWhenReady(ctx, func(ctx context.Context) error {
		clients = s.clients.All()
		return nil
	}); err != nil {
		return nil, err
	}
	return clients, nil
}

func (s *Scheduler) Addresses() []string {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.broadcastAddresses
}

func (s *Scheduler) StartApp(ctx context.Context) {
	s.cluster.StartApp()
}

func (s *Scheduler) StopApp(ctx context.Context) {
	s.cluster.StopApp()
}

func (s *Scheduler) callWhenReady(ctx context.Context, fn concurrency.Runner) error {
	for {
		s.lock.RLock()
		readyCh := s.readyCh
		s.lock.RUnlock()

		select {
		case <-s.disabled:
			return errors.New("scheduler not enabled")
		case <-ctx.Done():
			return ctx.Err()
		case <-readyCh:
		}

		// Ensure still ready as there is a race from above.
		s.lock.RLock()
		select {
		case <-s.readyCh:
			defer s.lock.RUnlock()
			return fn(ctx)
		default:
			s.lock.RUnlock()
		}
	}
}
