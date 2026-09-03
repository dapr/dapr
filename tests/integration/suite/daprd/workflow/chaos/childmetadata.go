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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childmetadata))
}

// childmetadata verifies a child's metadata is never served from a state older
// than the completion its parent already observed. A child dispatches its
// completion to the parent before committing its own terminal state; the hold
// below parks that commit, so a read that bypassed the child's actor would
// report the pre-turn shape (PENDING, no name).
type childmetadata struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (c *childmetadata) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.store = fault.New(t)
	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
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

func (c *childmetadata) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "cm-parent"
	const childID = "cm-child"

	reg := c.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))

	// The child's terminal commit is the only child Multi deleting its inbox.
	arrived, release, done := c.store.ArmMultiDeleteHold(childID + "||inbox-")
	t.Cleanup(release)

	client := c.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	select {
	case <-arrived:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the child's terminal commit never reached the store")
	}

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	type fetched struct {
		meta *backend.WorkflowMetadata
		err  error
	}
	fetchCh := make(chan fetched, 1)
	go func() {
		m, ferr := client.FetchWorkflowMetadata(ctx, childID)
		fetchCh <- fetched{m, ferr}
	}()

	select {
	case f := <-fetchCh:
		require.Failf(t, "child metadata served before its terminal commit",
			"status %s name %q err %v", f.meta.GetRuntimeStatus(), f.meta.GetName(), f.err)
	case <-time.After(time.Second):
	}

	release()
	<-done

	select {
	case f := <-fetchCh:
		require.NoError(t, f.err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, f.meta.GetRuntimeStatus())
		assert.Equal(t, "quick", f.meta.GetName())
	case <-time.After(time.Second * 20):
		require.Fail(t, "child metadata read did not return after the commit")
	}
}
