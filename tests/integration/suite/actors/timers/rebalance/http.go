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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	app1        *actors.Actors
	deactivated slice.Slice[string]
	fired1      slice.Slice[string]
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.deactivated = slice.String()
	h.fired1 = slice.String()

	h.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				h.deactivated.Append(r.PathValue("id"))
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			h.fired1.Append(r.PathValue("id"))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app1),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.app1.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	body := `{"dueTime":"0s","period":"1s","data":"hello"}`

	for i := range 100 {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/timers/foo", h.app1.Daprd().HTTPAddress(), i)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, h.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	fired2 := slice.String()
	app2 := actors.New(t,
		actors.WithPeerActor(h.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			fired2.Append(r.PathValue("id"))
		}),
	)
	t.Cleanup(func() { app2.Cleanup(t) })
	app2.Run(t, ctx)
	app2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		deactivated := h.deactivated.Len()
		assert.Positive(c, deactivated)
		gauge := h.app1.Daprd().Metrics(c, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
		assert.InDelta(c, float64(100-deactivated), gauge, 0)
	}, time.Second*20, time.Millisecond*10)

	moved := make(map[string]struct{})
	for _, id := range h.deactivated.Slice() {
		moved[id] = struct{}{}
	}
	before := h.fired1.Len()
	time.Sleep(time.Second * 3)

	after := h.fired1.Slice()
	assert.Greater(t, len(after), before, "timers of non-moved actors must keep firing")
	for _, id := range after[before:] {
		assert.NotContains(t, moved, id, "a moved actor's timer fired on its old host")
	}
	assert.Empty(t, fired2.Slice(), "a timer fire crossed to the actor's new host")
}
