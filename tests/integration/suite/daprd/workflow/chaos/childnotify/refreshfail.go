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
	suite.Register(new(refreshfail))
}

// refreshfail fails the child's first delivery so the retry reminder
// re-sends, then fails the metadata re-read that follows the save clearing
// the parent-notify row. The re-send loses its cached runtime state at that
// point and must still finish settling without it.
type refreshfail struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (r *refreshfail) Setup(t *testing.T) []framework.Option {
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

func (r *refreshfail) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const parentID = "refreshfail-p"
	const childID = "refreshfail-c"

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
		return "settled", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	// The Multi deleting the parent-notify row is the clearing save; the Get
	// that follows it is the metadata refresh to fail.
	var armed atomic.Bool
	failed := make(chan struct{})
	r.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		for _, op := range req.Operations {
			if d, ok := op.(state.DeleteRequest); ok && strings.Contains(d.Key, childID+"||parent-notify") && !armed.Load() {
				r.store.ArmGetFailures(childID+"||metadata", 1, failed)
				armed.Store(true)
			}
		}
	})

	client := r.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*20, time.Millisecond*10)

	// The first delivery fails, so the retry reminder drives the re-send
	// whose clearing save is the one refreshed.
	r.store.ArmFailures(parentID+"||inbox-", 1, nil)
	close(releaseCh)

	meta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"settled"`, meta.GetOutput().GetValue())
	select {
	case <-failed:
	case <-time.After(time.Second * 10):
		require.Fail(t, "the metadata refresh after the clearing save was never attempted")
	}

	// The sidecar survived the lost cache and the child is settled: it
	// answers, reads COMPLETED and is purgeable.
	cmeta, err := client.FetchWorkflowMetadata(ctx, childID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus())
	require.NoError(t, client.PurgeWorkflowState(ctx, childID))
}
