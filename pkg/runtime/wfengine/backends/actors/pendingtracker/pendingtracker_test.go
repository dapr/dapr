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

package pendingtracker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend/local"
)

// flakyBackend fails the first n cancel calls with a transient error before
// delegating, mimicking the cluster backend's remote fall-through failing.
type flakyBackend struct {
	Backend
	wfFails  atomic.Int32
	wfCalls  atomic.Int32
	actFails atomic.Int32
	actCalls atomic.Int32
}

func (f *flakyBackend) CancelWorkflowTask(ctx context.Context, id api.InstanceID) error {
	f.wfCalls.Add(1)
	if f.wfFails.Add(-1) >= 0 {
		return errors.New("transient cancel failure")
	}
	return f.Backend.CancelWorkflowTask(ctx, id)
}

func (f *flakyBackend) CancelActivityTask(ctx context.Context, id api.InstanceID, taskID int32) error {
	f.actCalls.Add(1)
	if f.actFails.Add(-1) >= 0 {
		return errors.New("transient cancel failure")
	}
	return f.Backend.CancelActivityTask(ctx, id, taskID)
}

func wfReq(iid string) *protos.WorkflowRequest {
	return &protos.WorkflowRequest{InstanceId: iid}
}

func actReq(iid string, taskID int32) *protos.ActivityRequest {
	return &protos.ActivityRequest{
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: iid},
		TaskId:           taskID,
	}
}

func Test_SetExecutorAvailableSweepsPending(t *testing.T) {
	t.Parallel()

	tr := New(local.NewTasksBackend())

	var wfErr, actErr error
	var wfCalls, actCalls int
	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(_ *protos.WorkflowResponse, err error) {
		wfCalls++
		wfErr = err
	})
	tr.OnActivityCompletion(actReq("wf1", 7), func(_ *protos.ActivityResponse, err error) {
		actCalls++
		actErr = err
	})

	// Registrations while available stay parked.
	assert.Zero(t, wfCalls)
	assert.Zero(t, actCalls)

	tr.SetExecutorAvailable(false)

	assert.Equal(t, 1, wfCalls)
	assert.Equal(t, 1, actCalls)
	require.ErrorIs(t, wfErr, api.ErrTaskCancelled)
	require.ErrorIs(t, actErr, api.ErrTaskCancelled)
}

func Test_RegistrationWhileUnavailableIsCancelledImmediately(t *testing.T) {
	t.Parallel()

	tr := New(local.NewTasksBackend())
	tr.SetExecutorAvailable(false)

	var wfErr, actErr error
	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(_ *protos.WorkflowResponse, err error) {
		wfErr = err
	})
	tr.OnActivityCompletion(actReq("wf1", 3), func(_ *protos.ActivityResponse, err error) {
		actErr = err
	})

	require.ErrorIs(t, wfErr, api.ErrTaskCancelled)
	require.ErrorIs(t, actErr, api.ErrTaskCancelled)
}

func Test_AvailableCompletionsFlowThrough(t *testing.T) {
	t.Parallel()

	tr := New(local.NewTasksBackend())

	var resp *protos.WorkflowResponse
	var wfErr error
	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(r *protos.WorkflowResponse, err error) {
		resp = r
		wfErr = err
	})

	require.NoError(t, tr.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1"}))
	require.NoError(t, wfErr)
	require.NotNil(t, resp)
	assert.Equal(t, "wf1", resp.GetInstanceId())
}

func Test_DeregisteredEntriesAreNotCancelled(t *testing.T) {
	t.Parallel()

	tr := New(local.NewTasksBackend())

	var wfCalls int
	dereg := tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(*protos.WorkflowResponse, error) {
		wfCalls++
	})
	dereg()

	tr.SetExecutorAvailable(false)
	assert.Zero(t, wfCalls)
}

func Test_SweepRetriesTransientCancelFailures(t *testing.T) {
	t.Parallel()

	flaky := &flakyBackend{Backend: local.NewTasksBackend()}
	flaky.wfFails.Store(2)
	flaky.actFails.Store(2)
	tr := New(flaky)

	var wfErr, actErr error
	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(_ *protos.WorkflowResponse, err error) {
		wfErr = err
	})
	tr.OnActivityCompletion(actReq("wf1", 7), func(_ *protos.ActivityResponse, err error) {
		actErr = err
	})

	tr.SetExecutorAvailable(false)

	require.ErrorIs(t, wfErr, api.ErrTaskCancelled,
		"a transient cancel failure must be retried, not dropped: an uncancelled turn wedges the unregister HaltAll")
	require.ErrorIs(t, actErr, api.ErrTaskCancelled)
	assert.Equal(t, int32(3), flaky.wfCalls.Load())
	assert.Equal(t, int32(3), flaky.actCalls.Load())
}

func Test_CancelErrorWithDeregisteredEntryIsNotRetried(t *testing.T) {
	t.Parallel()

	// The cancel error coincides with the waiter settling (completion won the
	// arbiter): the deregister removes the tracking entry, so the sweep must
	// treat the error as the benign race and stop after one attempt.
	inner := local.NewTasksBackend()
	var dereg func()
	var calls atomic.Int32
	tr := New(&deregOnCancelBackend{Backend: inner, calls: &calls, dereg: &dereg})

	dereg = tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(*protos.WorkflowResponse, error) {})

	tr.SetExecutorAvailable(false)
	assert.Equal(t, int32(1), calls.Load())
}

// deregOnCancelBackend errors every cancel and deregisters the waiter inside
// the first one, simulating a completion racing the sweep.
type deregOnCancelBackend struct {
	Backend
	calls *atomic.Int32
	dereg *func()
}

func (d *deregOnCancelBackend) CancelWorkflowTask(context.Context, api.InstanceID) error {
	if d.calls.Add(1) == 1 {
		(*d.dereg)()
	}
	return errors.New("cancel failed")
}

func Test_RetryStopsWhenExecutorReconnects(t *testing.T) {
	t.Parallel()

	flaky := &flakyBackend{Backend: local.NewTasksBackend()}
	flaky.wfFails.Store(1 << 30)
	tr := New(flaky)

	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(*protos.WorkflowResponse, error) {})

	done := make(chan struct{})
	go func() {
		defer close(done)
		tr.SetExecutorAvailable(false)
	}()

	time.Sleep(time.Millisecond * 120)
	tr.SetExecutorAvailable(true)

	select {
	case <-done:
	case <-time.After(time.Second * 5):
		t.Fatal("the sweep must stop retrying once an executor reconnects")
	}
}

func Test_SupersededDeregisterKeepsNewRegistrationTracked(t *testing.T) {
	t.Parallel()

	tr := New(local.NewTasksBackend())

	// First attempt registers, is superseded, and deregisters LATE, after the
	// second attempt for the same instance registered. The stale deregister
	// must not evict the newer attempt's tracking entry.
	deregOld := tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(*protos.WorkflowResponse, error) {})

	var newErr error
	var newCalls int
	tr.OnWorkflowTaskCompletion(wfReq("wf1"), func(_ *protos.WorkflowResponse, err error) {
		newCalls++
		newErr = err
	})

	deregOld()

	tr.SetExecutorAvailable(false)
	assert.Equal(t, 1, newCalls)
	require.ErrorIs(t, newErr, api.ErrTaskCancelled)
}
