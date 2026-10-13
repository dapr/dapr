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

package restart

import (
	"context"
	"net/http"
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
	suite.Register(new(reactivate))
}

// reactivate checks an actor gets a new activation id each time it activates,
// not once per daprd process. It keeps one id across calls, idles out, and
// gets a new id when it's next called.
type reactivate struct {
	app *actors.Actors

	lock   sync.Mutex
	events []string
}

func (a *reactivate) Setup(t *testing.T) []framework.Option {
	a.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}", func(http.ResponseWriter, *http.Request) {
			a.lock.Lock()
			defer a.lock.Unlock()
			a.events = append(a.events, "DELETE")
		}),
		actors.WithHandler("/actors/abc/{id}/method/{method}", func(_ http.ResponseWriter, r *http.Request) {
			a.lock.Lock()
			defer a.lock.Unlock()
			a.events = append(a.events, r.Header.Get("Dapr-Actor-Activation-Id"))
		}),
		actors.WithActorIdleTimeout(time.Second*2),
	)

	return []framework.Option{
		framework.WithProcesses(a.app),
	}
}

func (a *reactivate) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	seen := make(map[string]bool)
	for range 2 {
		a.invoke(t, ctx)
		a.invoke(t, ctx)
		ids := a.take()
		require.Len(t, ids, 2)
		require.NotEmpty(t, ids[0])
		assert.Equal(t, ids[0], ids[1])
		assert.False(t, seen[ids[0]])
		seen[ids[0]] = true

		var events []string
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			events = append(events, a.take()...)
			assert.Equal(c, []string{"DELETE"}, events)
		}, time.Second*8, time.Millisecond*10)
	}
}

func (a *reactivate) take() []string {
	a.lock.Lock()
	defer a.lock.Unlock()
	events := a.events
	a.events = nil
	return events
}

func (a *reactivate) invoke(t *testing.T, ctx context.Context) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := a.app.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "abc",
			ActorId:   "1",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*8, time.Millisecond*100)
}
