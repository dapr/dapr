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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(drop))
}

type drop struct {
	app1        *actors.Actors
	deactivated slice.Slice[string]
	fired1      slice.Slice[string]

	holding     chan struct{}
	holdRelease chan struct{}
	releaseOnce sync.Once
}

func (d *drop) Setup(t *testing.T) []framework.Option {
	d.deactivated = slice.String()
	d.fired1 = slice.String()
	d.holding = make(chan struct{})
	d.holdRelease = make(chan struct{})

	d.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithDrainRebalancedActors(true),
		actors.WithDrainOngoingCallTimeout(time.Second*5),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				d.deactivated.Append(r.PathValue("id"))
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/hold", func(nethttp.ResponseWriter, *nethttp.Request) {
			close(d.holding)
			<-d.holdRelease
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			d.fired1.Append(r.PathValue("id"))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(d.app1),
	}
}

func (d *drop) Run(t *testing.T, ctx context.Context) {
	d.app1.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { d.releaseOnce.Do(func() { close(d.holdRelease) }) })

	httpClient := client.HTTP(t)
	body := `{"dueTime":"0s","period":"100ms","data":"hello"}`

	for i := range 100 {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/timers/foo", d.app1.Daprd().HTTPAddress(), i)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, d.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	grpcClient := d.app1.GRPCClient(t, ctx)
	errCh := make(chan error, 1)
	go func() {
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "0",
			Method:    "hold",
		})
		errCh <- err
	}()
	select {
	case <-d.holding:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold call never reached the app")
	}

	fired2 := slice.String()
	app2 := actors.New(t,
		actors.WithPeerActor(d.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			fired2.Append(r.PathValue("id"))
		}),
	)
	t.Cleanup(func() { app2.Cleanup(t) })
	app2.Run(t, ctx)

	require.Eventually(t, func() bool {
		before := d.fired1.Len()
		time.Sleep(time.Millisecond * 300)
		return d.fired1.Len() == before
	}, time.Second*10, time.Millisecond*10, "timer fires never stalled on the dissemination lock")
	d.releaseOnce.Do(func() { close(d.holdRelease) })
	require.NoError(t, <-errCh)

	app2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		deactivated := d.deactivated.Len()
		assert.Positive(c, deactivated)
		metrics := d.app1.Daprd().Metrics(c, ctx)
		assert.Positive(c, metrics.SumWithLabels("dapr_runtime_actor_timers_dropped_total", "actor_type:abc"))
		assert.InDelta(c, float64(100-deactivated), metrics.SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc"), 0)
	}, time.Second*20, time.Millisecond*10)

	assert.Empty(t, fired2.Slice(), "a dropped timer fire crossed to the actor's new host")
}
