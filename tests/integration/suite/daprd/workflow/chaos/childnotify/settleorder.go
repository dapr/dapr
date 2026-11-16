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
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(settleorder))
}

// settleorder terminates a child whose parent cannot be reached: the cascade
// to the child's own children must still land, and must not wait behind the
// notification that keeps failing.
type settleorder struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (s *settleorder) Setup(t *testing.T) []framework.Option {
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

func (s *settleorder) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const parentID = "settleorder-p"
	const childID = "settleorder-c"
	const grandchildID = "settleorder-g"

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("grandchild", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("never", time.Hour).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("grandchild", task.WithChildWorkflowInstanceID(grandchildID)).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))

	client := s.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, client, grandchildID, api.RUNTIME_STATUS_RUNNING)

	// The parent's inbox rejects every write from here: the child's
	// notification keeps failing while the cascade to its own child must not.
	s.store.ArmFailures(parentID+"||inbox-", 1<<20, nil)
	require.NoError(t, client.TerminateWorkflow(ctx, childID))
	wf.WaitForRuntimeStatus(t, ctx, client, childID, api.RUNTIME_STATUS_TERMINATED)
	wf.WaitForRuntimeStatus(t, ctx, client, grandchildID, api.RUNTIME_STATUS_TERMINATED)

	meta, err := client.FetchWorkflowMetadata(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus(), "the parent is still owed the notification")
	assert.Positive(t, s.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"), "the retry reminder drives the notification")

	// Once reachable again the parent learns of the terminated child.
	s.store.ArmFailures(parentID+"||inbox-", 0, nil)
	meta, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
