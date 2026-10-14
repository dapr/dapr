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
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(strayafterdelivery))
}

// strayafterdelivery raises an event on a child whose completion the parent
// already acknowledged, while the parent's inbox is failing. The turn must
// not re-arm the notification: nothing is owed, so the child stays
// purgeable and no retry reminder appears.
type strayafterdelivery struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (s *strayafterdelivery) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	s.store = fault.New(t)
	sock := socket.New(t)
	s.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(s.store),
	)

	s.workflow = workflow.New(t,
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
`, s.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.ss, s.workflow),
	}
}

func (s *strayafterdelivery) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const parentID = "strayafter-p"
	const childID = "strayafter-c"

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		return "done", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return out, nil
	}))

	var markerWrites atomic.Int32
	s.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			if set, ok := op.(state.SetRequest); ok && strings.Contains(set.Key, childID+"||parent-notify") {
				markerWrites.Add(1)
			}
		}
	})

	client := s.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, client, childID, api.RUNTIME_STATUS_COMPLETED)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		completed, _ := wf.ChildCompletions(t, ctx, client, parentID, 0)
		assert.Equal(c, 1, completed, "the parent holds the completion")
	}, time.Second*20, time.Millisecond*10)
	require.Equal(t, int32(1), markerWrites.Load())

	// The parent is unreachable from here; a late event on the settled child
	// must not make it owe the notification again.
	s.store.ArmFailures(parentID+"||inbox-", 1<<20, nil)
	require.NoError(t, client.RaiseEvent(ctx, childID, "late"))
	time.Sleep(time.Second * 2)
	assert.Equal(t, int32(1), markerWrites.Load(), "the marker is written only by the completing turn")
	assert.Zero(t, s.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"))
	require.NoError(t, client.PurgeWorkflowState(ctx, childID), "nothing is owed, so the child is purgeable")

	s.store.ArmFailures(parentID+"||inbox-", 0, nil)
	require.NoError(t, client.RaiseEvent(ctx, parentID, "go"))
	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.JSONEq(t, `"done"`, meta.GetOutput().GetValue())
}
