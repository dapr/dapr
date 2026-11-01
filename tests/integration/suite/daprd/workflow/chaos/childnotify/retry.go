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
	"google.golang.org/protobuf/proto"

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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retry))
}

// retry fails the parent's inbox save once, so the child's post-commit
// notification fails after its terminal state is durable. The re-send must
// deliver exactly one completion, and the marker row lives until purge.
type retry struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (c *retry) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.store = fault.New(t)
	sock := socket.New(t)
	c.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(c.store),
	)

	c.workflow = workflow.New(t,
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
`, c.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.ss, c.workflow),
	}
}

func (c *retry) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "notifyretry-p"
	const childID = "notifyretry-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := c.workflow.Registry()
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

	var markerUpserts, markerDeletes, parentCompletions atomic.Int32
	c.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			switch v := op.(type) {
			case state.SetRequest:
				if strings.Contains(v.Key, childID+"||parent-notify") {
					markerUpserts.Add(1)
				}
				if strings.Contains(v.Key, parentID+"||history-") {
					if b, ok := v.Value.([]byte); ok {
						var e protos.HistoryEvent
						if proto.Unmarshal(b, &e) == nil && e.GetChildWorkflowInstanceCompleted() != nil {
							parentCompletions.Add(1)
						}
					}
				}
			case state.DeleteRequest:
				if strings.Contains(v.Key, childID+"||parent-notify") {
					markerDeletes.Add(1)
				}
			}
		}
	})

	client := c.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	// Armed only now, so the parent's own create and child-scheduling saves
	// are not the victims: the next parent inbox write is the child's
	// completion notification.
	failed := make(chan struct{})
	c.store.ArmFailures(parentID+"||inbox-", 1, failed)
	close(releaseCh)

	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's inbox save was never attempted")
	}

	// The child's terminal state is already durable when the notify fails.
	cmeta, err := client.FetchWorkflowMetadata(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus())

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"hello"`, meta.GetOutput().GetValue())

	assert.Equal(t, int32(1), markerUpserts.Load(), "the marker is written with the terminal commit")
	assert.Equal(t, int32(1), parentCompletions.Load(), "the parent commits exactly one child completion")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), markerDeletes.Load(), "the acknowledged delivery clears the marker")
	}, time.Second*20, time.Millisecond*10)

	require.NoError(t, client.PurgeWorkflowState(ctx, childID))
	assert.Equal(t, int32(1), markerDeletes.Load(), "nothing left for purge to delete")
}
