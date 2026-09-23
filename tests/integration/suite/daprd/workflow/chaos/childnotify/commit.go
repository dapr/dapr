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
	suite.Register(new(commit))
}

// commit verifies a child commits its terminal state before its parent
// learns of the completion: while the child's terminal save is held, the
// parent must not complete and no parent row may be written.
type commit struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (c *commit) Setup(t *testing.T) []framework.Option {
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

func (c *commit) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "commit-p"
	const childID = "commit-c"

	reg := c.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return "hello", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The child's terminal save is its only Multi deleting an inbox row.
	arrived, release, done := c.store.ArmMultiDeleteHold(childID + "||inbox-")
	t.Cleanup(release)

	var held atomic.Bool
	var parentWritesDuringHold atomic.Int32
	c.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		if !held.Load() {
			return
		}
		for _, op := range req.Operations {
			var key string
			switch v := op.(type) {
			case state.SetRequest:
				key = v.Key
			case state.DeleteRequest:
				key = v.Key
			}
			if strings.Contains(key, parentID+"||") {
				parentWritesDuringHold.Add(1)
			}
		}
	})

	client := c.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	select {
	case <-arrived:
		held.Store(true)
	case <-time.After(time.Second * 20):
		require.Fail(t, "the child's terminal commit never reached the store")
	}

	assert.Never(t, func() bool {
		meta, ferr := client.FetchWorkflowMetadata(ctx, parentID)
		return ferr == nil && api.WorkflowMetadataIsComplete(meta)
	}, time.Second*2, time.Millisecond*50, "the parent must not complete before the child's terminal state is committed")

	held.Store(false)
	release()
	<-done

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"hello"`, meta.GetOutput().GetValue())

	cmeta, err := client.FetchWorkflowMetadata(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus())
	assert.Equal(t, "quick", cmeta.GetName())

	assert.Zero(t, parentWritesDuringHold.Load(), "no parent row may be written while the child's commit is held")
}
