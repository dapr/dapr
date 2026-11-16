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

package addressreuse

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(originalreturns))
}

// originalreturns tests that an app which gets its internal gRPC address back
// after another app held it is reachable again. The caller still pools the
// connection it dialed for the original app on its first stay at the address,
// and that connection spent the other app's stay failing handshakes.
type originalreturns struct {
	sentry   *sentry.Sentry
	sched    *scheduler.Scheduler
	caller   *daprd.Daprd
	first    *daprd.Daprd
	other    *daprd.Daprd
	returned *daprd.Daprd

	firstCalled atomic.Int64
	otherCalled atomic.Int64
}

func (o *originalreturns) Setup(t *testing.T) []framework.Option {
	o.sentry = sentry.New(t)
	o.sched = scheduler.New(t,
		scheduler.WithSentry(o.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
	)

	firstApp := app.New(t,
		app.WithConfig(`{"entities": ["firsttype"]}`),
		app.WithHandlerFunc("/actors/firsttype/", func(nethttp.ResponseWriter, *nethttp.Request) {
			o.firstCalled.Add(1)
		}),
	)
	otherApp := app.New(t,
		app.WithConfig(`{"entities": ["othertype"]}`),
		app.WithHandlerFunc("/actors/othertype/", func(nethttp.ResponseWriter, *nethttp.Request) {
			o.otherCalled.Add(1)
		}),
	)

	opts := func(extra ...daprd.Option) []daprd.Option {
		return append([]daprd.Option{
			daprd.WithSentry(t, o.sentry),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithScheduler(o.sched),
		}, extra...)
	}

	o.caller = daprd.New(t, opts()...)
	o.first = daprd.New(t, opts(daprd.WithAppPort(firstApp.Port()))...)
	o.other = daprd.New(t, opts(
		daprd.WithAppPort(otherApp.Port()),
		daprd.WithInternalGRPCPort(o.first.InternalGRPCPort()),
	)...)
	o.returned = daprd.New(t, opts(
		daprd.WithAppID(o.first.AppID()),
		daprd.WithAppPort(firstApp.Port()),
		daprd.WithInternalGRPCPort(o.first.InternalGRPCPort()),
	)...)

	return []framework.Option{
		framework.WithProcesses(o.sentry, o.sched, firstApp, otherApp, o.caller),
	}
}

func (o *originalreturns) Run(t *testing.T, ctx context.Context) {
	o.sched.WaitUntilRunning(t, ctx)
	o.caller.WaitUntilRunning(t, ctx)

	client := o.caller.GRPCClient(t, ctx)
	invoke := func(c *assert.CollectT, actorType string) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: actorType,
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}

	start := func(d *daprd.Daprd) {
		d.Run(t, ctx)
		t.Cleanup(func() { d.Cleanup(t) })
		d.WaitUntilRunning(t, ctx)
	}

	start(o.first)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invoke(c, "firsttype")
	}, time.Second*20, time.Millisecond*10)
	o.first.Cleanup(t)

	start(o.other)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invoke(c, "othertype")
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, o.otherCalled.Load())
	o.other.Cleanup(t)

	before := o.firstCalled.Load()
	start(o.returned)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invoke(c, "firsttype")
	}, time.Second*20, time.Millisecond*10)
	assert.Greater(t, o.firstCalled.Load(), before)
}
