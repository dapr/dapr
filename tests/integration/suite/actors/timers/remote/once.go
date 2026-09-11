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

package remote

import (
	"context"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(once))
}

// once asserts a one shot timer is registered through the sidecar which owns
// the actor, is rejected by the other sidecar, and fires exactly once, on the
// owner, whichever host that is.
type once struct {
	app1 *actors.Actors
	app2 *actors.Actors

	lock  sync.Mutex
	fired map[string][]int
	owner map[string]int
}

func (o *once) record(path string, host int) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "actor-") {
		return
	}
	o.lock.Lock()
	defer o.lock.Unlock()
	switch {
	case strings.Contains(path, "/method/timer/"):
		o.fired[parts[2]] = append(o.fired[parts[2]], host)
	case strings.HasSuffix(path, "/method/foo"):
		o.owner[parts[2]] = host
	}
}

func (o *once) Setup(t *testing.T) []framework.Option {
	o.fired = make(map[string][]int)
	o.owner = make(map[string]int)

	o.app1 = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			o.record(r.URL.Path, 0)
		}),
	)

	o.app2 = actors.New(t,
		actors.WithPeerActor(o.app1),
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			o.record(r.URL.Path, 1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(o.app1, o.app2),
	}
}

func (o *once) Run(t *testing.T, ctx context.Context) {
	o.app1.WaitUntilRunning(t, ctx)
	o.app2.WaitUntilRunning(t, ctx)

	clients := []rtv1.DaprClient{
		o.app1.GRPCClient(t, ctx),
		o.app2.GRPCClient(t, ctx),
	}

	// Wait until both hosts agree on the placement table before requiring
	// exactly one owner per actor. The probe actor is excluded from the
	// firing records.
	probe := &rtv1.RegisterActorTimerRequest{
		ActorType: "abc",
		ActorId:   "probe",
		Name:      "t",
		DueTime:   "0s",
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err1 := clients[0].RegisterActorTimer(ctx, probe)
		_, err2 := clients[1].RegisterActorTimer(ctx, probe)
		assert.NotEqual(c, err1 == nil, err2 == nil)
	}, time.Second*20, time.Millisecond*10)

	// Timer operations are accepted only by the sidecar which owns the
	// actor. Each actor is activated first to learn its owner, then the
	// timer is registered through the owner after asserting the other
	// sidecar rejects it.
	const numIDs = 20
	for i := range numIDs {
		id := "actor-" + strconv.Itoa(i)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := clients[0].InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "abc",
				ActorId:   id,
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)

		o.lock.Lock()
		owner, ok := o.owner[id]
		o.lock.Unlock()
		require.True(t, ok, "actor %q was invoked but no host recorded it", id)

		req := &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   id,
			Name:      "t",
			DueTime:   "0s",
		}
		_, err := clients[1-owner].RegisterActorTimer(ctx, req)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		_, err = clients[owner].RegisterActorTimer(ctx, req)
		require.NoError(t, err)
	}

	// Every timer fires exactly once, on the host owning its actor, and both
	// hosts fire some.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		o.lock.Lock()
		defer o.lock.Unlock()
		assert.Len(c, o.fired, numIDs)
	}, time.Second*20, time.Millisecond*10)

	o.lock.Lock()
	defer o.lock.Unlock()
	hosts := make(map[int]int)
	for id, firings := range o.fired {
		require.Lenf(t, firings, 1, "timer for actor %q fired %d times", id, len(firings))
		require.Equal(t, o.owner[id], firings[0], "timer for actor %q fired on a host which does not own it", id)
		hosts[firings[0]]++
	}
	assert.Len(t, hosts, 2, "all timers fired on one host: cross-host ownership untested")
}
