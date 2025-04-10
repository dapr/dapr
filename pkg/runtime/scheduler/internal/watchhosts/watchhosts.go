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
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.watchhosts")

type Options struct {
	Addresses []string
	Security  security.Handler
}

type WatchHosts struct {
	allAddrs []string
	security security.Handler

	gotAddrs atomic.Pointer[ContextAddress]
	subs     *broadcaster.Broadcaster[*ContextAddress]

	cancel  context.CancelFunc
	readyCh chan struct{}
	lock    sync.Mutex
}

type ContextAddress struct {
	context.Context
	Addresses []string
}

func New(opts Options) *WatchHosts {
	return &WatchHosts{
		allAddrs: opts.Addresses,
		security: opts.Security,
		subs:     broadcaster.New[*ContextAddress](),
		readyCh:  make(chan struct{}),
	}
}

func (w *WatchHosts) Run(ctx context.Context) error {
	defer w.subs.Close()

	stream, closeCon, err := w.connSchedulerHosts(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to scheduler host: %s", err)
	}

	if stream != nil {
		defer func() {
			stream.CloseSend()
			closeCon()
		}()
	}

	err = w.handleStream(ctx, stream)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err != nil {
		return fmt.Errorf("failed to handle Scheduler WatchHosts stream: %s", err)
	}

	return nil
}

func (w *WatchHosts) Addresses(ctx context.Context) <-chan *ContextAddress {
	ch := make(chan *ContextAddress, 1)
	select {
	case <-ctx.Done():
		ch <- &ContextAddress{ctx, nil}
		return ch
	case <-w.readyCh:
	}

	w.lock.Lock()
	defer w.lock.Unlock()
	w.subs.Subscribe(ctx, ch)
	ch <- w.gotAddrs.Load()

	return ch
}

func (w *WatchHosts) handleStream(ctx context.Context, stream schedulerv1pb.Scheduler_WatchHostsClient) error {
	// If no stream was made the server doesn't support watching hosts
	// (pre-1.15), so we use static. Remove in 1.16.
	if stream == nil {
		w.lock.Lock()
		got := &ContextAddress{ctx, w.allAddrs}
		w.gotAddrs.Store(got)
		w.subs.Broadcast(got)
		w.lock.Unlock()
		close(w.readyCh)
		<-ctx.Done()
		return nil
	}

	for {
		gotAddrs, err := w.watchNextAddresses(ctx, stream)
		if err != nil {
			return err
		}

		w.lock.Lock()
		if w.cancel != nil {
			w.cancel()
		}

		actx, cancel := context.WithCancel(ctx)
		w.cancel = cancel
		got := &ContextAddress{actx, gotAddrs}
		w.gotAddrs.Store(got)
		w.subs.Broadcast(got)
		w.lock.Unlock()

		select {
		case <-w.readyCh:
		default:
			close(w.readyCh)
		}
	}
}

func (w *WatchHosts) watchNextAddresses(ctx context.Context, stream schedulerv1pb.Scheduler_WatchHostsClient) ([]string, error) {
	resp, err := stream.Recv()
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			// Ignore unimplemented error code as we are talking to an old server.
			// TODO: @joshvanl: remove special case in v1.16.
			return slices.Clone(w.allAddrs), nil
		}
		return nil, err
	}

	gotAddrs := make([]string, 0, len(resp.GetHosts()))
	for _, host := range resp.GetHosts() {
		gotAddrs = append(gotAddrs, host.GetAddress())
	}

	log.Infof("Received updated scheduler hosts addresses: %v", gotAddrs)

	return gotAddrs, nil
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
		if status.Code(err) == codes.Unimplemented {
			// Ignore unimplemented error code as we are talking to an old server.
			// TODO: @joshvanl: remove special case in v1.16.
			return nil, nil, nil
		}

		return nil, nil, fmt.Errorf("failed to watch scheduler hosts: %s", err)
	}

	return stream, closeCon, nil
}
