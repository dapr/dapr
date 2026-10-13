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
	"maps"
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
	suite.Register(new(httpcallbacks))
}

// httpcallbacks checks the activation id reaches an HTTP app as a header. A
// method, timer and reminder in one activation share an id, and the next
// activation after the actor idles out gets a new one.
type httpcallbacks struct {
	app *actors.Actors

	lock        sync.Mutex
	ids         map[string]string
	deactivated bool
}

func (h *httpcallbacks) Setup(t *testing.T) []framework.Option {
	h.ids = make(map[string]string)
	h.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Second*2),
		actors.WithHandler("/actors/abc/{id}", func(http.ResponseWriter, *http.Request) {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.deactivated = true
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ http.ResponseWriter, r *http.Request) {
			h.record("invoke", r)
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/{name}", func(_ http.ResponseWriter, r *http.Request) {
			h.record("timer", r)
		}),
		actors.WithHandler("/actors/abc/{id}/method/remind/{name}", func(_ http.ResponseWriter, r *http.Request) {
			h.record("reminder", r)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(h.app),
	}
}

func (h *httpcallbacks) Run(t *testing.T, ctx context.Context) {
	h.app.WaitUntilRunning(t, ctx)

	client := h.app.GRPCClient(t, ctx)
	invoke := func() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "abc",
				ActorId:   "1",
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*8, time.Millisecond*100)
	}

	invoke()
	_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc", ActorId: "1", Name: "t", DueTime: "0s", Period: "1s",
	})
	require.NoError(t, err)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc", ActorId: "1", Name: "r", DueTime: "0s", Period: "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, h.take(false), 3)
	}, time.Second*8, time.Millisecond*10)

	_, err = client.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{ActorType: "abc", ActorId: "1", Name: "t"})
	require.NoError(t, err)
	_, err = client.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{ActorType: "abc", ActorId: "1", Name: "r"})
	require.NoError(t, err)

	ids := h.take(true)
	require.Len(t, ids, 3)
	first := ids["invoke"]
	require.NotEmpty(t, first)
	assert.Equal(t, first, ids["timer"])
	assert.Equal(t, first, ids["reminder"])

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		h.lock.Lock()
		defer h.lock.Unlock()
		assert.True(c, h.deactivated)
	}, time.Second*8, time.Millisecond*10)
	h.take(true)

	invoke()
	next := h.take(true)["invoke"]
	require.NotEmpty(t, next)
	assert.NotEqual(t, first, next)
}

func (h *httpcallbacks) record(kind string, r *http.Request) {
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.ids[kind]; !ok {
		h.ids[kind] = r.Header.Get("Dapr-Actor-Activation-Id")
	}
}

func (h *httpcallbacks) take(reset bool) map[string]string {
	h.lock.Lock()
	defer h.lock.Unlock()
	ids := maps.Clone(h.ids)
	if reset {
		h.ids = make(map[string]string)
	}
	return ids
}
