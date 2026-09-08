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
	"encoding/json"
	"fmt"
	"io"
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
	suite.Register(new(reregister))
}

type reregister struct {
	app1        *actors.Actors
	deactivated slice.Slice[string]
	fired1      slice.Slice[string]
}

func (r *reregister) Setup(t *testing.T) []framework.Option {
	r.deactivated = slice.String()
	r.fired1 = slice.String()

	r.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, req *nethttp.Request) {
			if req.Method == nethttp.MethodDelete {
				r.deactivated.Append(req.PathValue("id"))
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, req *nethttp.Request) {
			r.fired1.Append(req.PathValue("id"))
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app1),
	}
}

func (r *reregister) Run(t *testing.T, ctx context.Context) {
	r.app1.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	body := `{"dueTime":"0s","period":"1s","data":"hello"}`

	for i := range 100 {
		url := fmt.Sprintf("http://%s/v1.0/actors/abc/%d/timers/foo", r.app1.Daprd().HTTPAddress(), i)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, r.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	fired2 := slice.String()
	app2 := actors.New(t,
		actors.WithPeerActor(r.app1),
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(_ nethttp.ResponseWriter, req *nethttp.Request) {
			fired2.Append(req.PathValue("id"))
		}),
	)
	t.Cleanup(func() { app2.Cleanup(t) })
	app2.Run(t, ctx)
	app2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, r.deactivated.Len())
	}, time.Second*10, time.Millisecond*10)

	moved := r.deactivated.Slice()[0]

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/%s/timers/foo", r.app1.Daprd().HTTPAddress(), moved)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusForbidden, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	var apiErr struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(respBody, &apiErr))
	assert.Equal(t, "ERR_ACTOR_TIMER_NOT_OWNED", apiErr.ErrorCode)

	url = fmt.Sprintf("http://%s/v1.0/actors/abc/%s/timers/foo", app2.Daprd().HTTPAddress(), moved)
	req, err = nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err = httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Contains(c, fired2.Slice(), moved)
	}, time.Second*10, time.Millisecond*10)

	gauge := app2.Daprd().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
	assert.InDelta(t, float64(1), gauge, 0)
}
