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

package chaos

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(earlycompletion))
}

type earlycompletion struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *earlycompletion) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)
	if !workflow.FastPathFromEnv() {
		t.Skip("the folded completion that lands behind the early one is a WorkflowsFastPath path")
	}
	e.store = fault.New(t)
	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)
	e.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		// The early completion has no signed scheduling to verify against.
		workflow.WithSigningDisabledN(0),
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, e.ss.SocketName())),
		),
	)
	return []framework.Option{
		framework.WithProcesses(e.ss, e.workflow),
	}
}

func (e *earlycompletion) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "earlycompletion-wf"

	step0Started := make(chan struct{}, 1)
	release0 := make(chan struct{})
	reg := e.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		var n int
		if err := actx.GetInput(&n); err != nil {
			return nil, err
		}
		if n != 0 {
			return n, nil
		}
		select {
		case step0Started <- struct{}{}:
		default:
		}
		select {
		case <-release0:
			return n, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("seq", func(ctx *task.WorkflowContext) (any, error) {
		var sum int
		for i := range 3 {
			var n int
			if err := ctx.CallActivity("step", task.WithActivityInput(i)).Await(&n); err != nil {
				return nil, err
			}
			sum += n
		}
		return sum, nil
	}))

	// The observer runs before its Multi: the first history save after step
	// 0 is released is the turn that dispatches step 1.
	var released, armed atomic.Bool
	failed := make(chan struct{})
	holdArmed := make(chan struct{})
	var holdArrived <-chan struct{}
	var holdRelease func()
	e.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok || !strings.Contains(set.Key, wfID+"||history-") {
				continue
			}
			if released.Load() && armed.CompareAndSwap(false, true) {
				e.store.ArmFailures(wfID+"||history-", 1, failed)
				holdArrived, holdRelease, _ = e.store.ArmMultiSetHold(wfID + "||inbox-")
				close(holdArmed)
			}
			return
		}
	})

	client := e.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(wfID))
	require.NoError(t, err)
	select {
	case <-step0Started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "step 0 never started")
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client.FetchWorkflowMetadata(ctx, wfID)
		if assert.NoError(c, merr) {
			assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
		}
	}, time.Second*20, time.Millisecond*10)

	released.Store(true)
	close(release0)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the injected commit failure never fired")
	}
	<-holdArmed
	t.Cleanup(func() { holdRelease() })
	select {
	case <-holdArrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the early step 1 completion never reached the inbox")
	}
	// The re-sent step 0 completion queues on the turn lock behind the hold.
	require.Eventually(t, func() bool {
		return e.workflow.Scheduler().JobKeyCount(t, ctx, "activity-result-") >= 1
	}, time.Second*20, time.Millisecond*10)
	time.Sleep(time.Millisecond * 500)
	holdRelease()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client.FetchWorkflowMetadata(ctx, wfID)
		if assert.NoError(c, merr) {
			assert.Equal(c, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), meta.GetFailureDetails().GetErrorMessage())
		}
	}, time.Second*20, time.Millisecond*10)
	meta, err := client.FetchWorkflowMetadata(ctx, wfID)
	require.NoError(t, err)
	assert.JSONEq(t, `3`, meta.GetOutput().GetValue())
}
