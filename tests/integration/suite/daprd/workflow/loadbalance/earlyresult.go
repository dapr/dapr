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

package loadbalance

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
	suite.Register(new(earlyresult))
}

// earlyresult makes an activity's completion reach the workflow before the
// TaskScheduled event that dispatched it is committed: the turn that
// dispatches step1 fails its save after the dispatch landed, so step1's
// completion is already in the durable inbox when the turn is replayed. The
// replay must record the schedule for the early completion instead of losing
// it, or the following turn fails the workflow with a non-determinism error.
type earlyresult struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *earlyresult) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	e.store = fault.New(t)
	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)

	e.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithFastPath(true),
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

func (e *earlyresult) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const id = "earlyresult"

	reg := e.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step0", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("step1", func(task.ActivityContext) (any, error) {
		return "one", nil
	}))
	require.NoError(t, reg.AddActivityN("step2", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("seq", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("step0").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("step1").Await(&out); err != nil {
			return nil, err
		}
		if err := ctx.CallActivity("step2").Await(nil); err != nil {
			return nil, err
		}
		return out, nil
	}))

	var historySaves atomic.Int32
	failed := make(chan struct{})
	e.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		var history, inboxDelete bool
		for _, op := range req.Operations {
			switch v := op.(type) {
			case state.SetRequest:
				history = history || strings.Contains(v.Key, id+"||history-")
			case state.DeleteRequest:
				inboxDelete = inboxDelete || strings.Contains(v.Key, id+"||inbox-")
			}
		}
		switch {
		case history && historySaves.Add(1) == 2:
			e.store.ArmFailures(id+"||history-", 1, failed)
		case inboxDelete && !history:
			e.store.ArmFailures(id+"||inbox-", 1, nil)
		}
	})

	client := e.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the turn dispatching step1 never saved")
	}

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), meta.GetFailureDetails().GetErrorMessage())
	assert.JSONEq(t, `"one"`, meta.GetOutput().GetValue())
}
