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

package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func TestStandby(t *testing.T) {
	newFactoryCtx := func(t *testing.T, ctx context.Context, standby bool) *factory {
		t.Helper()
		fact, err := New(ctx, Options{
			AppID:             "appID",
			ActivityActorType: "activity",
			WorkflowActorType: "workflow",
			Scheduler: func(context.Context, *backend.WorkflowWorkItem) error {
				return nil
			},
			Actors:   fake.New(),
			FastPath: true,
			Standby:  standby,
		})
		require.NoError(t, err)
		return fact.(*factory)
	}
	newFactory := func(t *testing.T, standby bool) *factory {
		t.Helper()
		return newFactoryCtx(t, t.Context(), standby)
	}

	t.Run("a standby host never takes the fast path", func(t *testing.T) {
		assert.True(t, newFactory(t, false).fastPath)
		assert.False(t, newFactory(t, true).fastPath)
	})

	t.Run("a standby host refuses the reminders that run a turn", func(t *testing.T) {
		o := newFactory(t, true).GetOrCreate("wf1").(*orchestrator)
		err := o.InvokeReminder(t.Context(), &api.Reminder{Name: "start-1"})
		require.Error(t, err)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.NoError(t, o.Deactivate(t.Context()))
	})

	t.Run("a standby host counts the operations it served until standby ends", func(t *testing.T) {
		assert.Nil(t, newFactory(t, false).standbyWaiting)

		ctx, cancel := context.WithCancel(t.Context())
		f := newFactoryCtx(t, ctx, true)
		require.NotNil(t, f.standbyWaiting)

		f.standbyServed(t.Context(), todo.CreateWorkflowInstanceMethod, nil)
		f.standbyServed(t.Context(), todo.PurgeWorkflowStateMethod, nil)
		assert.EqualValues(t, 1, f.standbyWaiting.waiting.Load())

		// The warning goroutine stops with the standby registration's ctx.
		cancel()
		done := make(chan struct{})
		go func() {
			f.bgWG.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			require.Fail(t, "standby goroutines did not stop")
		}
	})
}

func TestStandbyOperation(t *testing.T) {
	event := func(e *protos.HistoryEvent) *backend.HistoryEvent { return e }
	for _, tc := range []struct {
		method string
		ev     *backend.HistoryEvent
		want   string
	}{
		{todo.CreateWorkflowInstanceMethod, nil, diag.CreateWorkflow},
		{todo.PurgeWorkflowStateMethod, nil, diag.PurgeWorkflow},
		{todo.RecursivePurgeWorkflowStateMethod, nil, diag.PurgeWorkflow},
		{todo.RerunWorkflowInstance, nil, diag.RerunWorkflow},
		{todo.ForkWorkflowHistory, nil, diag.ForkWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{}}}), diag.RaiseEvent},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionTerminated{ExecutionTerminated: &protos.ExecutionTerminatedEvent{}}}), diag.TerminateWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}}}), diag.SuspendWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionResumed{ExecutionResumed: &protos.ExecutionResumedEvent{}}}), diag.ResumeWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_TimerFired{TimerFired: &protos.TimerFiredEvent{}}}), diag.AddEvent},
	} {
		assert.Equal(t, tc.want, standbyOperation(tc.method, tc.ev), tc.method)
	}
}

func TestStandbyWaiting(t *testing.T) {
	type warning struct {
		appID   string
		waiting int64
		since   time.Duration
	}

	clock := clocktesting.NewFakeClock(time.Now())
	warnings := make(chan warning, 10)
	w := newStandbyWaiting("myapp")
	w.clock = clock
	w.warn = func(appID string, waiting int64, since time.Duration) {
		warnings <- warning{appID, waiting, since}
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		w.run(ctx)
		close(done)
	}()

	// step advances the clock to elapsed since the start, once run waits on
	// its next warning.
	var elapsed time.Duration
	step := func(to time.Duration) {
		t.Helper()
		require.Eventually(t, clock.HasWaiters, 10*time.Second, time.Millisecond)
		clock.Step(to - elapsed)
		elapsed = to
	}
	noWarning := func() {
		t.Helper()
		require.Eventually(t, clock.HasWaiters, 10*time.Second, time.Millisecond)
		select {
		case got := <-warnings:
			require.Failf(t, "unexpected warning", "%+v", got)
		default:
		}
	}

	// Nothing served: no warning at the threshold.
	step(standbyWaitingWarnAfter)
	noWarning()

	// A purge completes on a standby host, so it is not waiting.
	w.served(diag.PurgeWorkflow)
	step(2 * standbyWaitingWarnAfter)
	noWarning()

	w.served(diag.CreateWorkflow)
	w.served(diag.RaiseEvent)

	// Warnings come at doubling intervals since the start, capped at
	// standbyWaitingWarnMaxInterval: 5m and 10m (passed above), 20m, 40m,
	// 80m, 140m.
	for _, at := range []time.Duration{20, 40, 80, 140} {
		at *= time.Minute
		step(at - time.Second)
		noWarning()
		step(at)
		select {
		case got := <-warnings:
			assert.Equal(t, warning{"myapp", 2, at}, got)
		case <-time.After(10 * time.Second):
			require.Failf(t, "no warning", "at %s", at)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "run did not stop")
	}
}
