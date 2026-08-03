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

package watchhosts

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.watchhosts")

type Options struct {
	Addresses  []string
	Healthz    healthz.Healthz
	Security   security.Handler
	Clients    *clients.Clients
	HostLoop   loop.Interface[loops.EventHost]
	Leadership *leadership.Leadership
}

type WatchHosts struct {
	allAddrs   []string
	htarget    healthz.Target
	security   security.Handler
	clients    *clients.Clients
	leadership *leadership.Leadership

	loop loop.Interface[loops.EventHost]
}

func New(opts Options) *WatchHosts {
	return &WatchHosts{
		htarget:    opts.Healthz.AddTarget("scheduler-watch-hosts"),
		allAddrs:   opts.Addresses,
		security:   opts.Security,
		clients:    opts.Clients,
		loop:       opts.HostLoop,
		leadership: opts.Leadership,
	}
}

func (w *WatchHosts) Run(ctx context.Context) error {
	if len(w.allAddrs) == 0 ||
		(len(w.allAddrs) == 1 && strings.TrimSpace(strings.Trim(w.allAddrs[0], `"'`)) == "") {
		w.clients.Disable()
		log.Warnf("No scheduler host addresses provided. Scheduler disabled")
		w.htarget.Ready()
		<-ctx.Done()

		return nil
	}

	for {
		stream, closeCon, err := w.connSchedulerHosts(ctx)
		if err != nil {
			log.Errorf("Failed to connect to scheduler host: %s", err)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retry.Jitter(time.Second, time.Second/2)):
				continue
			}
		}

		resp, err := stream.Recv()
		if status.Code(err) == codes.Unimplemented {
			if err = w.clients.Reload(ctx, w.allAddrs); err != nil {
				closeCon()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Errorf("Failed to reload scheduler clients, retrying: %s", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retry.Jitter(time.Second, time.Second/2)):
					continue
				}
			}

			// Ignore unimplemented error code as we are talking to an old server.
			// TODO: @joshvanl: remove special case in v1.16.
			if w.leadership != nil {
				w.leadership.SetUnsupported()
			}
			w.loop.Enqueue(&loops.ReloadClients{
				Addresses: slices.Clone(w.allAddrs),
			})
			closeCon()
			w.htarget.Ready()
			<-ctx.Done()

			return nil
		}

		// Keep receiving on the stream: the scheduler re-broadcasts the host
		// list on every membership or placement leadership change.
		for {
			if err != nil {
				closeCon()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Warnf("Scheduler WatchHosts stream error, reconnecting: %s", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
				break
			}

			if err = w.handleHosts(ctx, resp); err != nil {
				closeCon()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Errorf("Failed to reload scheduler clients, retrying: %s", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retry.Jitter(time.Second, time.Second/2)):
				}
				break
			}

			w.htarget.Ready()

			resp, err = stream.Recv()
		}
	}
}

func (w *WatchHosts) handleHosts(ctx context.Context, resp *schedulerv1pb.WatchHostsResponse) error {
	gotAddrs := make([]string, 0, len(resp.GetHosts()))
	leader := ""
	capable := false
	for _, host := range resp.GetHosts() {
		gotAddrs = append(gotAddrs, host.GetAddress())
		if host.GetLeader() {
			leader = host.GetAddress()
		}
		if host.GetPlacementEnabled() {
			capable = true
		}
	}

	log.Infof("Received scheduler hosts addresses: %v (placement leader %q)", gotAddrs, leader)

	if err := w.clients.Reload(ctx, gotAddrs); err != nil {
		return err
	}

	w.loop.Enqueue(&loops.ReloadClients{
		Addresses: gotAddrs,
	})

	if w.leadership != nil {
		if !capable && len(gotAddrs) > 0 {
			// No scheduler host in the cluster serves placement: either old
			// schedulers or placement disabled everywhere. Consumers fall
			// back to the standalone placement service.
			w.leadership.SetUnsupported()
		} else {
			w.leadership.Set(leader)
		}
	}

	return nil
}

func (w *WatchHosts) connSchedulerHosts(ctx context.Context) (schedulerv1pb.Scheduler_WatchHostsClient, context.CancelFunc, error) {
	//nolint:gosec
	i := rand.Intn(len(w.allAddrs))
	log.Debugf("Attempting to connect to scheduler to WatchHosts: %s", w.allAddrs[i])

	// This is connecting to a DNS A rec which will return healthy scheduler
	// hosts.
	cl, closeCon, err := client.New(ctx, w.allAddrs[i], w.security)
	if err != nil {
		return nil, nil, fmt.Errorf("scheduler client not initialized for address %s: %s", w.allAddrs[i], err)
	}

	stream, err := cl.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to watch scheduler hosts: %s", err)
	}

	return stream, closeCon, nil
}
