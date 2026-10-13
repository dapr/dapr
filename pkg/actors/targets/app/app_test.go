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

package app

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/channel/fake"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

func newTimerTestActor(t *testing.T, fn func(ctx context.Context, method string)) *app {
	t.Helper()
	fact := New(Options{
		ActorType:   "abc",
		Reentrancy:  reentrancystore.New(),
		IdleTimeout: time.Hour,
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		AppChannel: fake.New().WithInvokeMethod(func(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
			fn(ctx, req.Message().GetMethod())
			return invokev1.NewInvokeMethodResponse(http.StatusOK, "", nil), nil
		}),
	})
	return fact.GetOrCreate("foo").(*app)
}

func newRemovableTimer() (*api.Reminder, context.CancelFunc) {
	removed, remove := context.WithCancel(context.Background())
	return &api.Reminder{ActorType: "abc", ActorID: "foo", Name: "tick", IsTimer: true, Removed: removed}, remove
}

func TestInvokeTimerAlreadyRemovedDropped(t *testing.T) {
	t.Parallel()

	var timerCalls atomic.Int64
	a := newTimerTestActor(t, func(context.Context, string) { timerCalls.Add(1) })

	timer, remove := newRemovableTimer()
	remove()
	require.ErrorIs(t, a.InvokeTimer(t.Context(), timer), actorerrors.ErrTimerFireStale)
	assert.Zero(t, timerCalls.Load())
}

func TestInvokeTimerRemovedDuringCallbackCompletes(t *testing.T) {
	t.Parallel()

	timer, remove := newRemovableTimer()
	var called, alive atomic.Bool
	a := newTimerTestActor(t, func(ctx context.Context, _ string) {
		called.Store(true)
		remove()
		alive.Store(ctx.Err() == nil)
	})

	require.NoError(t, a.InvokeTimer(t.Context(), timer))
	assert.True(t, called.Load())
	assert.True(t, alive.Load(), "removing the timer cancelled its running callback")
}
