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
	suite.Register(new(differentapp))
}

// differentapp tests that an actor call succeeds when the internal gRPC
// address of a stopped app is taken over by a different app, as when
// Kubernetes hands a pod IP to another app. The caller's pooled connection to
// that address pins the old app's SPIFFE ID, so it must not be handed out for
// the new app.
type differentapp struct {
	sentry *sentry.Sentry
	sched  *scheduler.Scheduler
	caller *daprd.Daprd
	old    *daprd.Daprd
	new    *daprd.Daprd

	oldCalled atomic.Int64
	newCalled atomic.Int64
}

func (d *differentapp) Setup(t *testing.T) []framework.Option {
	d.sentry = sentry.New(t)
	d.sched = scheduler.New(t,
		scheduler.WithSentry(d.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
	)

	oldApp := app.New(t,
		app.WithConfig(`{"entities": ["oldtype"]}`),
		app.WithHandlerFunc("/actors/oldtype/", func(nethttp.ResponseWriter, *nethttp.Request) {
			d.oldCalled.Add(1)
		}),
	)
	newApp := app.New(t,
		app.WithConfig(`{"entities": ["newtype"]}`),
		app.WithHandlerFunc("/actors/newtype/", func(nethttp.ResponseWriter, *nethttp.Request) {
			d.newCalled.Add(1)
		}),
	)

	opts := func(extra ...daprd.Option) []daprd.Option {
		return append([]daprd.Option{
			daprd.WithSentry(t, d.sentry),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithScheduler(d.sched),
		}, extra...)
	}

	d.caller = daprd.New(t, opts()...)
	d.old = daprd.New(t, opts(daprd.WithAppPort(oldApp.Port()))...)
	d.new = daprd.New(t, opts(
		daprd.WithAppPort(newApp.Port()),
		daprd.WithInternalGRPCPort(d.old.InternalGRPCPort()),
	)...)

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.sched, oldApp, newApp, d.caller),
	}
}

func (d *differentapp) Run(t *testing.T, ctx context.Context) {
	d.sched.WaitUntilRunning(t, ctx)
	d.caller.WaitUntilRunning(t, ctx)

	d.old.Run(t, ctx)
	t.Cleanup(func() { d.old.Cleanup(t) })
	d.old.WaitUntilRunning(t, ctx)

	client := d.caller.GRPCClient(t, ctx)
	invoke := func(c *assert.CollectT, actorType string) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: actorType,
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}

	// Pools a connection from the caller to the old app's address.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invoke(c, "oldtype")
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, d.oldCalled.Load())

	d.old.Cleanup(t)

	d.new.Run(t, ctx)
	t.Cleanup(func() { d.new.Cleanup(t) })
	d.new.WaitUntilRunning(t, ctx)
	require.Equal(t, d.old.InternalGRPCAddress(), d.new.InternalGRPCAddress())

	// Reusing the pooled connection would fail every call with "unexpected
	// SPIFFE ID" until the connection idles out (DAPR_GRPC_MAX_CONN_IDLE, 3
	// minutes), well past this deadline.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		invoke(c, "newtype")
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, d.newCalled.Load())
}
