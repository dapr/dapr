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
	suite.Register(new(lostdriver))
}

// lostdriver leaves a completed child with an undelivered notification and no
// reminder left to re-send it. The next event to reach the child, a plain
// raise, must re-send it rather than ack past it.
type lostdriver struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (l *lostdriver) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)
	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)

	l.workflow = workflow.New(t,
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
`, l.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.ss, l.workflow),
	}
}

func (l *lostdriver) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const parentID = "lostdriver-p"
	const childID = "lostdriver-c"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	reg := l.workflow.Registry()
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
		return "late", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	var markerDeletes atomic.Int32
	l.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			if d, ok := op.(state.DeleteRequest); ok && strings.Contains(d.Key, childID+"||parent-notify") {
				markerDeletes.Add(1)
			}
		}
	})

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	failed := make(chan struct{})
	l.store.ArmFailures(parentID+"||inbox-", 1<<20, failed)
	close(releaseCh)
	select {
	case <-failed:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the parent's inbox save was never attempted")
	}
	wf.WaitForRuntimeStatus(t, ctx, client, childID, api.RUNTIME_STATUS_COMPLETED)

	// Lose every reminder the child has, the retry driver included: what a
	// scheduler that lost its data leaves behind. Under the fast path the
	// failed turn is retried locally for a few seconds and every retry
	// re-arms the retry reminder, so let that drain before sweeping.
	sched := l.workflow.Scheduler()
	sched.WaitJobKeyCount(t, ctx, "parent-notify", func(n int) bool { return n > 0 })
	time.Sleep(time.Second * 8)
	etcd := sched.ETCDClient(t, ctx)
	deleteChildJobs := func() {
		for _, key := range sched.ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.Contains(key, childID) {
				_, derr := etcd.Delete(ctx, key)
				require.NoError(t, derr)
			}
		}
	}
	deleteChildJobs()
	time.Sleep(time.Second)
	deleteChildJobs()
	require.Zero(t, sched.JobKeyCount(t, ctx, childID))
	l.store.ArmFailures(parentID+"||inbox-", 0, nil)

	// Nothing is left to deliver it: the parent stays where it is.
	time.Sleep(time.Second * 2)
	meta, err := client.FetchWorkflowMetadata(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
	assert.Zero(t, markerDeletes.Load())

	// A late event on the completed child drives a turn, which must carry
	// the pending notification with it.
	require.NoError(t, client.RaiseEvent(ctx, childID, "late"))
	meta, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"late"`, meta.GetOutput().GetValue())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), markerDeletes.Load(), "the acknowledged delivery clears the marker")
	}, time.Second*20, time.Millisecond*10)
}
