/*
Copyright 2026 The Dapr Authors
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

package rebalance

import (
	"context"
	"fmt"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(sweep))
}

// sweep checks that timer fires parked behind a rebalance drain are dropped once their timers are swept.
type sweep struct {
	app1   *actors.Actors
	fired1 slice.Slice[string]
	stale  slice.Slice[string]

	lock        sync.Mutex
	deactivated map[string]bool

	holding     chan struct{}
	holdingOnce sync.Once
	holdRelease chan struct{}
	releaseOnce sync.Once
}

func (s *sweep) Setup(t *testing.T) []framework.Option {
	s.fired1 = slice.String()
	s.stale = slice.String()
	s.deactivated = make(map[string]bool)
	s.holding = make(chan struct{})
	s.holdRelease = make(chan struct{})

	s.app1 = actors.New(t,
		actors.WithPlacement(placement.New(t, placement.WithDisseminateTimeout(time.Second))),
		actors.WithActorTypes("abc"),
		actors.WithDrainRebalancedActors(true),
		actors.WithDrainOngoingCallTimeout(time.Second*3),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				s.lock.Lock()
				s.deactivated[r.PathValue("id")] = true
				s.lock.Unlock()
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/hold", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			s.holdingOnce.Do(func() { close(s.holding) })
			select {
			case <-s.holdRelease:
			case <-r.Context().Done():
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			id := r.PathValue("id")
			s.fired1.Append(id)
			s.lock.Lock()
			defer s.lock.Unlock()
			if s.deactivated[id] {
				s.stale.Append(id)
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.app1),
	}
}

func (s *sweep) Run(t *testing.T, ctx context.Context) {
	const n = 20

	s.app1.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { s.releaseOnce.Do(func() { close(s.holdRelease) }) })

	httpClient := client.HTTP(t)
	createTimer := func(id, body string) int {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%s/timers/foo", s.app1.Daprd().HTTPAddress(), id)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}

	for i := range n {
		require.Equal(t, nethttp.StatusNoContent, createTimer(strconv.Itoa(i), `{"dueTime":"0s","period":"100ms"}`))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, s.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	grpcClient := s.app1.GRPCClient(t, ctx)
	errCh := make(chan error, 1)
	go func() {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "0",
			Method:    "hold",
		})
		errCh <- err
	}()
	t.Cleanup(func() {
		select {
		case <-errCh:
		case <-time.After(time.Second * 10):
		}
	})
	select {
	case <-s.holding:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold call never reached the app")
	}

	fired2 := slice.String()
	app2 := actors.New(t,
		actors.WithPeerActor(s.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			fired2.Append(r.PathValue("id"))
		}),
	)
	t.Cleanup(func() { app2.Cleanup(t) })
	app2.Run(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		s.lock.Lock()
		defer s.lock.Unlock()
		assert.NotEmpty(c, s.deactivated)
	}, time.Second*8, time.Millisecond*10)
	s.releaseOnce.Do(func() { close(s.holdRelease) })

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		s.lock.Lock()
		deactivated := len(s.deactivated)
		s.lock.Unlock()
		assert.Equal(c, n, deactivated)
		metrics := s.app1.Daprd().Metrics(c, ctx)
		assert.Zero(c, metrics.SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc"))
	}, time.Second*8, time.Millisecond*10)

	// Creating a timer parks until the next round unlocks and flushes the parked fires.
	assert.Contains(t, []int{nethttp.StatusNoContent, nethttp.StatusForbidden}, createTimer("fresh", `{"dueTime":"1h"}`))
	require.Eventually(t, func() bool {
		before := s.fired1.Len()
		time.Sleep(time.Millisecond * 500)
		return s.fired1.Len() == before
	}, time.Second*10, time.Millisecond*10)

	assert.Empty(t, s.stale.Slice(), "a swept timer fired after its actor was deactivated")
	metrics := s.app1.Daprd().Metrics(t, ctx)
	assert.Positive(t, metrics.SumWithLabels("dapr_runtime_actor_timers_dropped_total", "actor_type:abc"))
	assert.LessOrEqual(t, metrics.SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc"), float64(1))
	assert.Empty(t, fired2.Slice(), "a swept timer was re-registered on the actor's new host")
}
