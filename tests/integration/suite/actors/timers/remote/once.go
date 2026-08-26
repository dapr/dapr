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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(once))
}

// once asserts a one shot timer registered through one sidecar fires exactly
// once, on the single host which owns the actor, whichever host that is.
type once struct {
	app1 *actors.Actors
	app2 *actors.Actors

	lock  sync.Mutex
	fired map[string][]int
}

func (o *once) record(path string, host int) {
	if !strings.Contains(path, "/method/timer/") {
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return
	}
	o.lock.Lock()
	defer o.lock.Unlock()
	o.fired[parts[2]] = append(o.fired[parts[2]], host)
}

func (o *once) Setup(t *testing.T) []framework.Option {
	o.fired = make(map[string][]int)

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

	client := o.app1.GRPCClient(t, ctx)

	// Timers are registered for every actor ID through sidecar 1 only. Each
	// registration activates the actor on its owner, which may be either
	// host.
	const numIDs = 20
	for i := range numIDs {
		id := "actor-" + strconv.Itoa(i)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
				ActorType: "abc",
				ActorId:   id,
				Name:      "t",
				DueTime:   "0s",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}

	// Every timer fires exactly once, on exactly one host, and both hosts
	// fire some: the timer executed on the actor's owner regardless of which
	// sidecar registered it.
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
		hosts[firings[0]]++
	}
	assert.Len(t, hosts, 2, "all timers fired on one host: cross-host timer routing untested")
}
