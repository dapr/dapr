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

package call

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(down))
}

type down struct {
	place *down
	sched *down

	app1 *actors.Actors
	app2 *actors.Actors
	app3 *actors.Actors

	called atomic.Int32
}

func (d *down) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	ff := func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		d.called.Add(1)
		time.Sleep(time.Second / 4)
	}

	d.app1 = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	}, extra...)...)

	d.app2 = actors.New(t,
		actors.WithPeerActor(d.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", ff),
	)

	d.app3 = actors.New(t,
		actors.WithPeerActor(d.app1),
	)

	return []process.Interface{d.app1, d.app2, d.app3}
}

func (d *down) Setup(t *testing.T) []framework.Option {
	d.place, d.sched = new(down), new(down)
	procs := d.place.setup(t)
	procs = append(procs, d.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (d *down) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { d.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { d.sched.run(t, ctx) })
}

func (d *down) run(t *testing.T, ctx context.Context) {
	d.app1.WaitUntilRunning(t, ctx)
	d.app2.WaitUntilRunning(t, ctx)
	d.app3.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/123/method/foo", d.app3.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)

	for range 10 {
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	d.app2.Daprd().Cleanup(t)

	for range 10 {
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	assert.GreaterOrEqual(t, d.called.Load(), int32(20))
}
