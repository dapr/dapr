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

package activity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/detached"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
)

func newInvokeReq(meta map[string][]string) *internalsv1pb.InternalInvokeRequest {
	req := internalsv1pb.NewInternalInvokeRequest(todo.ExecuteActivityMethod)
	if meta != nil {
		req = req.WithMetadata(meta)
	}
	return req
}

// stubScheduler is a minimal scheduler.Interface capturing reminder creates.
type stubScheduler struct {
	lock      sync.Mutex
	creates   []*actorapi.CreateReminderRequest
	createErr error
}

func (s *stubScheduler) Close() error { return nil }
func (s *stubScheduler) Get(context.Context, *actorapi.GetReminderRequest) (*actorapi.Reminder, error) {
	return nil, nil
}

func (s *stubScheduler) Create(_ context.Context, req *actorapi.CreateReminderRequest) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.creates = append(s.creates, req)
	return nil
}
func (s *stubScheduler) Delete(context.Context, *actorapi.DeleteReminderRequest) error { return nil }
func (s *stubScheduler) DeleteByActorID(context.Context, *actorapi.DeleteRemindersByActorIDRequest) error {
	return nil
}

func (s *stubScheduler) List(context.Context, *actorapi.ListRemindersRequest) ([]*actorapi.Reminder, error) {
	return nil, nil
}

func (s *stubScheduler) snapshotCreates() []*actorapi.CreateReminderRequest {
	s.lock.Lock()
	defer s.lock.Unlock()
	return append([]*actorapi.CreateReminderRequest(nil), s.creates...)
}

type driveHarness struct {
	fact  *factory
	sched *stubScheduler

	lock      sync.Mutex
	callErr   error
	calls     []*actorapi.Reminder
	cancelOn1 bool // cancel driveCtx from inside the first CallReminder
}

func (h *driveHarness) snapshotCalls() []*actorapi.Reminder {
	h.lock.Lock()
	defer h.lock.Unlock()
	return append([]*actorapi.Reminder(nil), h.calls...)
}

func newDriveHarness(t *testing.T) *driveHarness {
	t.Helper()
	h := &driveHarness{sched: &stubScheduler{}}

	fakeRouter := routerfake.New().WithCallReminderFn(func(ctx context.Context, rem *actorapi.Reminder) error {
		h.lock.Lock()
		h.calls = append(h.calls, rem)
		cancelNow := h.cancelOn1 && len(h.calls) == 1
		err := h.callErr
		h.lock.Unlock()
		if cancelNow {
			h.fact.driveCancel()
			return context.Canceled
		}
		return err
	})

	driveCtx, driveCancel := context.WithCancel(t.Context())
	h.fact = &factory{
		appID:             "testapp",
		actorType:         "dapr.internal.default.testapp.activity",
		workflowActorType: "dapr.internal.default.testapp.workflow",
		router:            fakeRouter,
		reminders:         h.sched,
		fastPath:          true,
		driveCtx:          driveCtx,
		driveCancel:       driveCancel,
		rootCtx:           t.Context(),
		detached:          detached.New(t.Context()),
	}
	return h
}

const testActivityName = "act"

func testInvocation() *protos.ActivityInvocation {
	return &protos.ActivityInvocation{
		HistoryEvent: &protos.HistoryEvent{
			EventId: 3,
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: testActivityName},
			},
		},
	}
}

func Test_localDrive_successNoReminder(t *testing.T) {
	t.Parallel()
	h := newDriveHarness(t)

	a := h.fact.GetOrCreate("wf::3").(*activity)
	name := testActivityName
	require.True(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name))

	assert.Eventually(t, func() bool {
		return len(h.snapshotCalls()) == 1
	}, time.Second*5, time.Millisecond*10)
	h.fact.driveWG.Wait()

	call := h.snapshotCalls()[0]
	assert.Equal(t, activityReminderName, call.Name)
	assert.Equal(t, "wf::3", call.ActorID)
	assert.True(t, call.SkipRetries, "the drive owns its recovery; the router's blind retries must be skipped")
	assert.False(t, call.SkipLock, "the execution claim must take the activity actor lock")
	assert.NotNil(t, call.Data, "the invocation must ride on the synthetic reminder")

	assert.Empty(t, h.sched.snapshotCreates(), "a successful drive must not create any reminder")
}

func Test_localDrive_haltedFactoryFallsBack(t *testing.T) {
	t.Parallel()
	h := newDriveHarness(t)
	h.fact.driveCancel()

	a := h.fact.GetOrCreate("wf::3").(*activity)
	name := testActivityName
	assert.False(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name),
		"a halting factory must refuse the drive so the caller creates the durable reminder")
	assert.Empty(t, h.snapshotCalls())
}

func Test_driveActivity_escalatesAfterRetries(t *testing.T) {
	t.Parallel()
	h := newDriveHarness(t)
	h.callErr = errors.New("engine busy")

	a := h.fact.GetOrCreate("wf::3").(*activity)
	name := testActivityName
	require.True(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name))

	assert.Eventually(t, func() bool {
		return len(h.sched.snapshotCreates()) == 1
	}, time.Second*10, time.Millisecond*10, "a failed drive must escalate to the durable reminder")
	h.fact.driveWG.Wait()
	h.fact.detached.Wait()

	assert.Len(t, h.snapshotCalls(), localDriveMaxAttempts, "the drive retries at the reminder failure-policy cadence before escalating")

	create := h.sched.snapshotCreates()[0]
	assert.Equal(t, activityReminderName, create.Name)
	assert.Equal(t, "wf::3", create.ActorID)
	assert.Equal(t, h.fact.actorType, create.ActorType)
	require.NotNil(t, create.ConcurrencyKey)
	assert.Equal(t, testActivityName, *create.ConcurrencyKey)
}

func Test_driveActivity_escalatesImmediatelyOnCancel(t *testing.T) {
	t.Parallel()
	h := newDriveHarness(t)
	h.cancelOn1 = true

	a := h.fact.GetOrCreate("wf::3").(*activity)
	name := testActivityName
	require.True(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name))

	assert.Eventually(t, func() bool {
		return len(h.sched.snapshotCreates()) == 1
	}, time.Second*5, time.Millisecond*10, "a cancelled drive escalates without local retries: the reminder create is host-agnostic")
	h.fact.driveWG.Wait()
	h.fact.detached.Wait()

	assert.Len(t, h.snapshotCalls(), 1, "driveCtx cancellation must not be retried locally")
}

func Test_escalateActivity_skippedOnShutdown(t *testing.T) {
	t.Parallel()
	h := newDriveHarness(t)

	rootCtx, rootCancel := context.WithCancel(t.Context())
	rootCancel()
	h.fact.rootCtx = rootCtx
	h.fact.detached = detached.New(rootCtx)

	name := testActivityName
	h.fact.escalateActivity("wf::3", testInvocation(), time.Now().Add(-time.Second), &name)
	h.fact.detached.Wait()

	assert.Empty(t, h.sched.snapshotCreates(), "process shutdown must not spawn escalations; the janitor re-dispatches on the next owner")
}

func Test_localDriveCertified(t *testing.T) {
	t.Parallel()
	assert.False(t, localDriveCertified(newInvokeReq(nil)))
	assert.False(t, localDriveCertified(newInvokeReq(map[string][]string{"localDrive": {"false"}})))
	assert.False(t, localDriveCertified(newInvokeReq(map[string][]string{"localDrive": {}})))
	assert.True(t, localDriveCertified(newInvokeReq(map[string][]string{"localDrive": {"true"}})))
}
