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

package childnotify

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
	suite.Register(new(parentreplay))
}

type parentreplay struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (p *parentreplay) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.store = fault.New(t)
	sock := socket.New(t)
	p.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(p.store),
	)

	p.workflow = workflow.New(t,
		workflow.WithMTLS(t),
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
`, p.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.ss, p.workflow),
	}
}

func (p *parentreplay) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const parentID = "parentreplay-p"
	const childID = "parentreplay-c"

	var runs atomic.Int32
	releaseCh := make(chan struct{})
	reg := p.workflow.Registry()
	require.NoError(t, reg.AddActivityN("once", func(actx task.ActivityContext) (any, error) {
		runs.Add(1)
		select {
		case <-releaseCh:
			return nil, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("once").Await(nil); err != nil {
			return nil, err
		}
		return "replayed", nil
	}))
	require.NoError(t, reg.AddActivityN("noop", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		// A first turn that commits, so the create call returns before the
		// turn that dispatches the child is made to fail.
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The child's create save is the parent's dispatch; every parent history
	// save from then on, the one that would commit the creation included,
	// fails until the child's completion has been refused and dropped, which
	// the child records by deleting its parent-notify row.
	var armed atomic.Bool
	failed := make(chan struct{})
	dropped := make(chan struct{})
	var droppedOnce atomic.Bool
	p.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			switch v := op.(type) {
			case state.SetRequest:
				if strings.Contains(v.Key, childID+"||inbox-") && !armed.Load() {
					p.store.ArmFailures(parentID+"||history-", 1<<20, failed)
					armed.Store(true)
				}
			case state.DeleteRequest:
				if strings.Contains(v.Key, childID+"||parent-notify") && droppedOnce.CompareAndSwap(false, true) {
					close(dropped)
				}
			}
		}
	})

	client := p.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's commit of the creation was never attempted")
	}

	// The child completes and notifies a parent that has not committed the
	// creation; the parent refuses the completion and the child drops it.
	require.Eventually(t, func() bool { return runs.Load() == 1 }, time.Second*20, time.Millisecond*10)
	close(releaseCh)
	select {
	case <-dropped:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the child never dropped its refused completion")
	}
	assert.Zero(t, p.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"), "nothing is left to re-send it")

	// The parent can commit again: its replay re-dispatches the creation.
	p.store.ArmFailures(parentID+"||history-", 0, nil)
	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err, "the parent must receive the completion after its replay")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"replayed"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(1), runs.Load(), "the replayed creation must not run the child again")
}
