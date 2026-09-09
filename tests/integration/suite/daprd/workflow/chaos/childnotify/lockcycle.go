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
	suite.Register(new(lockcycle))
}

type lockcycle struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (l *lockcycle) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)
	l.store = fault.New(t)
	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)
	l.workflow = workflow.New(t,
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
`, l.ss.SocketName())),
		),
	)
	return []framework.Option{
		framework.WithProcesses(l.ss, l.workflow),
	}
}

func (l *lockcycle) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const parentID = "lockcycle-p"
	const childID = "lockcycle-c"

	var runs atomic.Int32
	releaseCh := make(chan struct{})
	reg := l.workflow.Registry()
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

	// Replays park on the gate with the parent's turn lock held.
	var passes atomic.Int32
	gate := make(chan struct{})
	gateReached := make(chan struct{}, 1)
	var gateOpen atomic.Bool
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		if passes.Add(1) > 1 && !gateOpen.Load() {
			select {
			case gateReached <- struct{}{}:
			default:
			}
			<-gate
		}
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	var armed atomic.Bool
	failed := make(chan struct{})
	childDone := make(chan struct{})
	var childDoneOnce atomic.Bool
	l.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			set, ok := op.(state.SetRequest)
			if !ok {
				continue
			}
			if strings.Contains(set.Key, childID+"||inbox-") && !armed.Load() {
				l.store.ArmFailures(parentID+"||history-", 1<<20, failed)
				armed.Store(true)
			}
			if strings.Contains(set.Key, childID+"||parent-notify") && childDoneOnce.CompareAndSwap(false, true) {
				close(childDone)
			}
		}
	})

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's commit of the creation was never attempted")
	}
	select {
	case <-gateReached:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent never replayed past the failed commit")
	}
	require.Eventually(t, func() bool { return runs.Load() == 1 }, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	select {
	case <-childDone:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the child never committed its completion")
	}

	l.store.ArmFailures(parentID+"||history-", 0, nil)
	gateOpen.Store(true)
	close(gate)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := client.FetchWorkflowMetadata(ctx, parentID)
		if assert.NoError(c, merr) {
			assert.Equal(c, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		}
	}, time.Second*15, time.Millisecond*10, "the parent's dispatch into the completing child and the child's notification must not wait on each other")
	meta, err := client.FetchWorkflowMetadata(ctx, parentID)
	require.NoError(t, err)
	assert.JSONEq(t, `"replayed"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(1), runs.Load(), "the replayed creation must not run the child again")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, l.workflow.Scheduler().JobKeyCount(c, ctx, "parent-notify"), "nothing is left to re-send")
	}, time.Second*10, time.Millisecond*10)
}
