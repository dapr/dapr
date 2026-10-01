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
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(latereschedule))
}

// latereschedule makes a workflow schedule a task again a turn after the
// turn that first dispatched it failed its save. The turn folding step1's
// completion dispatches step2 and then fails to save, so step1's completion
// is nacked back to its activity, which re-delivers it through a result
// reminder held back for a few seconds. step2's completion reaches the
// workflow first, in a turn that does not schedule step2, so the result is
// dropped. Once step1's completion is re-delivered, the workflow schedules
// step2 under a new TaskExecutionId, which dispatches it again: the workflow
// completes with the result of step2's second run.
type latereschedule struct {
	workflow   *workflow.Workflow
	ss         *statestore.StateStore
	store      *fault.Store
	step2Calls atomic.Int32
}

func (l *latereschedule) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)
	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)

	l.workflow = workflow.New(t,
		// The injected save failure rolls back signed rows mid-commit; under
		// history signing the retried completion would then read as tampering
		// (as in savefail).
		workflow.WithSigningDisabledN(0),
		workflow.WithNoDB(),
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_TEST_ACTIVITY_RESULT_DELAY", "3s",
			)),
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
`, l.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.ss, l.workflow),
	}
}

func (l *latereschedule) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const id = "latereschedule"

	reg := l.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step1", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("step2", func(task.ActivityContext) (any, error) {
		l.step2Calls.Add(1)
		return "two", nil
	}))
	require.NoError(t, reg.AddWorkflowN("seq", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("step1").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("step2").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// Fail the second history save: the turn that takes step1's completion
	// and dispatches step2.
	var historySaves atomic.Int32
	failed := make(chan struct{})
	l.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			if set, ok := op.(state.SetRequest); ok && strings.Contains(set.Key, id+"||history-") {
				if historySaves.Add(1) == 2 {
					l.store.ArmFailures(id+"||history-", 1, failed)
				}
				return
			}
		}
	})

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-failed:
	case <-time.After(20 * time.Second):
		require.Fail(t, "the turn dispatching step2 never saved")
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := client.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), meta.GetFailureDetails().GetErrorMessage())
	assert.JSONEq(t, `"two"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(2), l.step2Calls.Load(), "step2 runs again for its new scheduling")
}
