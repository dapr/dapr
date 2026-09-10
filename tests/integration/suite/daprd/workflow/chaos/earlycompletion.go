/*
Copyright 2025 The Dapr Authors
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
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
	e.store = fault.New(t)

	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)

	e.workflow = workflow.New(t,
		workflow.WithFastPath(true),
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
	const steps = 5
	var calls atomic.Int64
	started := make(chan struct{}, 1)
	releaseAct := make(chan struct{})

	reg := e.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		if calls.Add(1) != 1 {
			return nil, nil
		}
		// Park the first call so the fault is armed while the instance is
		// quiescent: the next history commit is then the turn that consumes
		// this completion and dispatches the next activity.
		started <- struct{}{}
		select {
		case <-releaseAct:
			return nil, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		for range steps {
			if err := octx.CallActivity("step").Await(nil); err != nil {
				return nil, err
			}
		}
		return "ok", nil
	}))

	cl := e.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(wfID), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the first activity never ran")
	}

	// Lose the commit of the turn that dispatches the second activity, so that
	// activity runs with no TaskScheduled of its own in history and its
	// completion races the first one's redelivery into a single work item.
	failed := make(chan struct{})
	e.store.ArmFailures(wfID+"||history-", 1, failed)
	close(releaseAct)
	select {
	case <-failed:
	case <-time.After(time.Second * 30):
		require.Fail(t, "the injected commit failure never fired")
	}

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"an early completion was discarded, so the replay failed the workflow: %s", meta.GetFailureDetails().GetErrorMessage())
	assert.Equal(t, `"ok"`, meta.GetOutput().GetValue())
}
