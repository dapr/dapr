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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpcallbacks))
}

// httpcallbacks checks the activation id header on HTTP callbacks.
type httpcallbacks struct {
	app *actors.Actors

	rec         activation.Recorder
	deactivated atomic.Bool
}

func (h *httpcallbacks) Setup(t *testing.T) []framework.Option {
	h.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorIdleTimeout(time.Second*2),
		actors.WithHandler("/actors/abc/{id}", func(http.ResponseWriter, *http.Request) {
			h.deactivated.Store(true)
		}),
		actors.WithHandler("/actors/abc/{id}/method/foo", func(_ http.ResponseWriter, r *http.Request) {
			h.rec.Record("invoke", activation.FromHeader(r))
		}),
		actors.WithHandler("/actors/abc/{id}/method/timer/{name}", func(_ http.ResponseWriter, r *http.Request) {
			h.rec.Record("timer", activation.FromHeader(r))
		}),
		actors.WithHandler("/actors/abc/{id}/method/remind/{name}", func(_ http.ResponseWriter, r *http.Request) {
			h.rec.Record("reminder", activation.FromHeader(r))
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
		activation.Invoke(t, ctx, client, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})
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
		assert.Len(c, activation.FirstIDs(h.rec.Peek()), 3)
	}, time.Second*8, time.Millisecond*10)

	_, err = client.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{ActorType: "abc", ActorId: "1", Name: "t"})
	require.NoError(t, err)
	_, err = client.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{ActorType: "abc", ActorId: "1", Name: "r"})
	require.NoError(t, err)

	ids := activation.FirstIDs(h.rec.Take())
	require.Len(t, ids, 3)
	first := ids["invoke"]
	require.NotEmpty(t, first)
	assert.Equal(t, first, ids["timer"])
	assert.Equal(t, first, ids["reminder"])

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, h.deactivated.Load())
	}, time.Second*8, time.Millisecond*10)
	h.rec.Take()

	invoke()
	next := activation.FirstIDs(h.rec.Take())["invoke"]
	require.NotEmpty(t, next)
	assert.NotEqual(t, first, next)
}
