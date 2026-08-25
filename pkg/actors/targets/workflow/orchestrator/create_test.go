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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// createHarness captures every state save and reminder create in a single
// ordered operation log so tests can assert what a create attempt persisted.
type createHarness struct {
	lock sync.Mutex
	ops  []string

	orch *orchestrator
}

func newCreateHarness(t *testing.T, instanceID string) *createHarness {
	t.Helper()

	h := new(createHarness)

	fakeRems := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, "create:"+req.Name)
			return nil
		})

	fakeState := statefake.New().
		WithGetFn(func(context.Context, *actorapi.GetStateRequest, bool) (*actorapi.StateResponse, error) {
			// No stored state: fresh instance.
			return &actorapi.StateResponse{}, nil
		}).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, "save")
			return nil
		})

	actors := fake.New().
		WithReminders(func(context.Context) (reminders.Interface, error) {
			return fakeRems, nil
		}).
		WithState(func(context.Context) (actorstate.Interface, error) {
			return fakeState, nil
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
	})
	require.NoError(t, err)

	h.orch = fact.GetOrCreate(instanceID).(*orchestrator)

	return h
}

func startEventFor(instanceID string, ts time.Time, mutate func(*protos.ExecutionStartedEvent)) *backend.HistoryEvent {
	es := &protos.ExecutionStartedEvent{
		Name:  "TestWorkflow",
		Input: wrapperspb.String(`"in"`),
		WorkflowInstance: &protos.WorkflowInstance{
			InstanceId: instanceID,
		},
	}
	if mutate != nil {
		mutate(es)
	}
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(ts),
		EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: es},
	}
}

// primeActiveStart primes the orchestrator with an active, not-completed
// state: empty history, the given start event in the inbox.
func (h *createHarness) primeActiveStart(savedStart *backend.HistoryEvent) *wfenginestate.State {
	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	state.AddToInbox(savedStart)

	h.orch.state = state
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(h.orch.actorID, nil, nil)
	h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, savedStart.GetExecutionStarted())
	return state
}

func createRequestBytes(t *testing.T, startEvent *backend.HistoryEvent) []byte {
	t.Helper()
	b, err := proto.Marshal(&backend.CreateWorkflowInstanceRequest{StartEvent: startEvent})
	require.NoError(t, err)
	return b
}

// parentWithExec returns a startEventFor mutate hook attaching a
// ParentInstanceInfo whose WorkflowInstance carries the given ExecutionId, or
// none when execID is empty.
func parentWithExec(execID string) func(*protos.ExecutionStartedEvent) {
	return func(es *protos.ExecutionStartedEvent) {
		pi := &protos.ParentInstanceInfo{
			TaskScheduledId:  3,
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
		if execID != "" {
			pi.WorkflowInstance.ExecutionId = wrapperspb.String(execID)
		}
		es.ParentInstance = pi
	}
}

func Test_createWorkflowInstance_sameParentSameExecutionDedups(t *testing.T) {
	const instanceID = "test-parent-same-exec"

	h := newCreateHarness(t, instanceID)
	h.primeActiveStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec("exec-a")))

	// A crash-replay duplicate carries the same parent ExecutionId and is
	// ignored exactly as when no ExecutionId is present at all.
	incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-a"))
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
}

func Test_createWorkflowInstance_parentExecutionMismatchAlreadyExists(t *testing.T) {
	const instanceID = "test-parent-exec-mismatch"

	// A differing parent ExecutionId means the parent continued-as-new (or was
	// recreated) and scheduled a genuinely new child colliding with a live
	// child of a previous execution. It must fail with AlreadyExists so the
	// parent's child task is faulted, never silently deduplicated.
	h := newCreateHarness(t, instanceID)
	h.primeActiveStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec("exec-a")))

	incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-b"))
	err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "previous execution")

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops, "a colliding create from a new parent execution must not save or re-arm the old execution's start")
}

func Test_createWorkflowInstance_parentExecutionNilEitherSideDedups(t *testing.T) {
	const instanceID = "test-parent-exec-nil"

	// A missing ExecutionId on either side (older persisted state, rerun-path
	// creations that omit it) must keep today's conservative dedup.
	for name, execs := range map[string]struct{ saved, incoming string }{
		"saved set, incoming nil": {saved: "exec-a", incoming: ""},
		"saved nil, incoming set": {saved: "", incoming: "exec-b"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCreateHarness(t, instanceID)
			h.primeActiveStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec(execs.saved)))

			incoming := startEventFor(instanceID, time.Now(), parentWithExec(execs.incoming))
			require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

			h.lock.Lock()
			defer h.lock.Unlock()
			assert.Empty(t, h.ops)
		})
	}
}

func Test_sameParentExecution(t *testing.T) {
	pi := func(execID string) *protos.ParentInstanceInfo {
		p := &protos.ParentInstanceInfo{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
		if execID != "" {
			p.WorkflowInstance.ExecutionId = wrapperspb.String(execID)
		}
		return p
	}

	tests := map[string]struct {
		existing *protos.ParentInstanceInfo
		incoming *protos.ParentInstanceInfo
		want     bool
	}{
		"both nil":  {existing: pi(""), incoming: pi(""), want: true},
		"a nil":     {existing: pi(""), incoming: pi("x"), want: true},
		"b nil":     {existing: pi("x"), incoming: pi(""), want: true},
		"equal":     {existing: pi("x"), incoming: pi("x"), want: true},
		"different": {existing: pi("x"), incoming: pi("y"), want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, sameParentExecution(tc.existing, tc.incoming))
		})
	}
}
