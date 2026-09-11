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
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(commitcancel))
}

// commitcancel pins that a turn's commit is not abandoned when the host
// cancels the caller carrying it. A turn dispatches its activities BEFORE it
// saves, so a worker disconnect (which unregisters the actor types and cancels
// every in-flight turn) landing on a commit in flight would leave the side
// effects done and the history unwritten. The next work item then arrives out
// of order and the replay fails the workflow as non-deterministic.
type commitcancel struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (c *commitcancel) Setup(t *testing.T) []framework.Option {
	c.store = fault.New(t)

	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
		// The turn must run on the local drive loop, whose context HaltAll
		// cancels when the worker disconnects.
		workflow.WithFastPath(true),
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
`, c.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ss, c.workflow),
	}
}

func (c *commitcancel) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const wfID = "commitcancel-wf"
	var calls atomic.Int64
	started := make(chan struct{}, 1)
	releaseAct := make(chan struct{})

	reg := c.workflow.Registry()
	require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		if calls.Add(1) != 1 {
			return nil, nil
		}
		// Park the first call so the instance is quiescent while the hold is
		// armed: the next history commit is then the turn that consumes this
		// completion, which has already dispatched the second activity.
		started <- struct{}{}
		select {
		case <-releaseAct:
			return nil, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		for range 2 {
			if err := octx.CallActivity("step").Await(nil); err != nil {
				return nil, err
			}
		}
		return "ok", nil
	}))

	cl := client.NewTaskHubGrpcClient(c.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	workerCtx, workerCancel := context.WithCancel(ctx)
	require.NoError(t, cl.StartWorkItemListener(workerCtx, reg))
	t.Cleanup(workerCancel)

	id, err := cl.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(wfID), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the first activity never ran")
	}
	arrived, release := c.store.ArmMultiHold(wfID + "||history-")
	t.Cleanup(release)
	close(releaseAct)
	select {
	case <-arrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "no turn commit was captured")
	}

	// The only worker leaves while that commit is in flight: daprd
	// unregisters its workflow actor types and cancels the turn.
	workerCancel()
	assert.Never(t, func() bool { return c.store.MultiCancelled() > 0 }, time.Second*3, time.Millisecond*10,
		"the commit was abandoned when the host cancelled its caller")
	release()

	require.NoError(t, cl.StartWorkItemListener(ctx, reg))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Zero(t, c.store.MultiCancelled())
}
