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
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reusewait))
}

// reusewait starts a workflow, through the client API, under the ID of a
// completed child whose parent has not acknowledged the completion yet. The
// child refuses the start until the acknowledgement frees the ID, so the
// start waits for it and then succeeds, rather than fail.
type reusewait struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *reusewait) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.store = fault.New(t)
	sock := socket.New(t)
	r.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(r.store),
	)

	r.workflow = workflow.New(t,
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

func (r *reusewait) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const parentID = "reusewait-p"
	const childID = "reusewait-c"

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
		return "reused", nil
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

	// Every parent inbox save fails from here on, so the notification cannot
	// land until the fault is disarmed.
	failed := make(chan struct{})
	r.store.ArmFailures(parentID+"||inbox-", 1<<20, failed)
	close(releaseCh)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's inbox save was never attempted")
	}
	wf.WaitForRuntimeStatus(t, ctx, client, childID, api.RUNTIME_STATUS_COMPLETED)

	startedAfter := time.Now()
	startErr := make(chan error, 1)
	go func() {
		_, serr := client.ScheduleNewWorkflow(ctx, "child", api.WithInstanceID(childID))
		startErr <- serr
	}()
	select {
	case serr := <-startErr:
		require.Failf(t, "the start returned before the parent acknowledged the completion", "error: %v", serr)
	case <-time.After(time.Second * 2):
	}

	r.store.ArmFailures(parentID+"||inbox-", 0, nil)
	select {
	case serr := <-startErr:
		require.NoError(t, serr, "the start must succeed once the acknowledgement frees the ID")
	case <-time.After(time.Second * 30):
		require.Fail(t, "the start did not return after the parent acknowledged the completion")
	}

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"reused"`, meta.GetOutput().GetValue())

	cmeta, err := client.WaitForWorkflowCompletion(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus())
	assert.True(t, cmeta.GetCreatedAt().AsTime().After(startedAfter), "the child ID runs a new workflow")
}
