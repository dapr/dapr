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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(idle))
}

type idle struct {
	app         *actors.Actors
	deactivated atomic.Int64
	fired       atomic.Int64
}

func (i *idle) Setup(t *testing.T) []framework.Option {
	i.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Second),
		actors.WithHandler("/actors/abc/{id}", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				i.deactivated.Add(1)
			}
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/foo", func(nethttp.ResponseWriter, *nethttp.Request) {
			i.fired.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(i.app),
	}
}

func (i *idle) Run(t *testing.T, ctx context.Context) {
	i.app.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/foo/timers/foo", i.app.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url,
		strings.NewReader(`{"dueTime":"0s","period":"3s","data":"hello"}`))
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, i.fired.Load())
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, i.deactivated.Load())
	}, time.Second*10, time.Millisecond*10)

	fired := i.fired.Load()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, i.fired.Load(), fired)
	}, time.Second*10, time.Millisecond*10)

	gauge := i.app.Daprd().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
	assert.InDelta(t, float64(1), gauge, 0)
}
