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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(restart))
}

// restart keeps the parent's inbox save failing so the child's notification
// cannot land, restarts the sidecar with the child committed, then lets the
// store recover: the durable marker and its reminder must deliver the
// completion exactly once after the restart.
type restart struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.store = fault.New(t)
	sock := socket.New(t)
	r.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(r.store),
	)

	r.workflow = workflow.New(t,
		// Keep signing off: the armed fault rolls back a signed row mid-commit
		// and the retried write would be classified as tampering.
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
`, r.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.ss, r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const parentID = "restart-p"
	const childID = "restart-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := r.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gate", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-releaseCh:
			return nil, nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("gate").Await(nil); err != nil {
			return nil, err
		}
		return "hello", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := r.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	// Every parent inbox save fails from here: the child commits, its
	// notification cannot land, and it arms the durable retry reminder.
	r.store.ArmFailures(parentID+"||inbox-", 1<<20, nil)
	close(releaseCh)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, ferr := client.FetchWorkflowMetadata(ctx, childID)
		if assert.NoError(c, ferr) {
			assert.Equal(c, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		}
		assert.Positive(c, r.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"))
	}, time.Second*20, time.Millisecond*10)

	r.workflow.Dapr().RestartGraceful(t, ctx)
	r.workflow.WaitUntilRunning(t, ctx)
	r.store.ArmFailures(parentID+"||inbox-", 0, nil)

	client = r.workflow.BackendClient(t, ctx)
	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"hello"`, meta.GetOutput().GetValue())

	hist, err := client.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	var completions int
	for _, e := range hist.GetEvents() {
		if e.GetChildWorkflowInstanceCompleted() != nil {
			completions++
		}
	}
	assert.Equal(t, 1, completions)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, r.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"))
	}, time.Second*20, time.Millisecond*10)
}
