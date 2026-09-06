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
	suite.Register(new(pending))
}

// pending keeps the parent's inbox failing so the child's completion stays
// undelivered: the child reads COMPLETED, but purging it or reusing its id is
// refused until the parent acknowledges, after which both succeed.
type pending struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (p *pending) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	p.store = fault.New(t)
	sock := socket.New(t)
	p.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(p.store),
	)

	p.workflow = workflow.New(t,
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
`, p.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.ss, p.workflow),
	}
}

func (p *pending) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const parentID = "notifypending-p"
	const childID = "notifypending-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := p.workflow.Registry()
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

	var markerDeletes atomic.Int32
	p.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			if d, ok := op.(state.DeleteRequest); ok && strings.Contains(d.Key, childID+"||parent-notify") {
				markerDeletes.Add(1)
			}
		}
	})

	client := p.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	// Every parent inbox save fails from here on, so the notification cannot
	// land until the fault is disarmed.
	failed := make(chan struct{})
	p.store.ArmFailures(parentID+"||inbox-", 1<<20, failed)
	close(releaseCh)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's inbox save was never attempted")
	}

	cmeta, err := client.FetchWorkflowMetadata(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus())

	err = client.PurgeWorkflowState(ctx, childID)
	require.ErrorContains(t, err, api.ErrNotCompleted.Error(), "an unacknowledged completion is not purgeable")
	// Refused as Unavailable, which the client retries: bound the attempt.
	reuseCtx, reuseCancel := context.WithTimeout(ctx, time.Second*3)
	t.Cleanup(reuseCancel)
	_, err = client.ScheduleNewWorkflow(reuseCtx, "child", api.WithInstanceID(childID))
	require.Error(t, err, "an unacknowledged completion blocks id reuse")
	wf.WaitForRuntimeStatus(t, ctx, client, childID, api.RUNTIME_STATUS_COMPLETED)
	assert.Zero(t, markerDeletes.Load())

	p.store.ArmFailures(parentID+"||inbox-", 0, nil)

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"hello"`, meta.GetOutput().GetValue())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), markerDeletes.Load(), "the acknowledged delivery clears the marker")
	}, time.Second*20, time.Millisecond*10)

	require.NoError(t, client.PurgeWorkflowState(ctx, childID))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, p.workflow.Scheduler().JobKeyCount(t, ctx, "parent-notify"))
	}, time.Second*20, time.Millisecond*10)
}
