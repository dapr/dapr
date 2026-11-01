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

package activityresult

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(lagread))
}

type lagread struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (l *lagread) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.store = fault.New(t)
	sock := socket.New(t)
	l.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(l.store),
	)

	l.workflow = workflow.New(t,
		workflow.WithFastPath(true),
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
			// The first janitor fire lands well after the refused publish, so
			// only the publish can consume the empty read.
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_JANITOR_PERIOD", "4s",
				"DAPR_WORKFLOW_REAPER_SCAN_INTERVAL", "100ms",
				"DAPR_WORKFLOW_REAPER_IDLE_TTL", "300ms",
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.ss, l.workflow),
	}
}

func (l *lagread) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const id = "activityresult-lagread"

	var runs atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	reg := l.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gate", func(actx task.ActivityContext) (any, error) {
		if runs.Add(1) > 1 {
			return "rerun", nil
		}
		close(entered)
		select {
		case <-release:
			return "first", nil
		case <-actx.Context().Done():
			return nil, actx.Context().Err()
		}
	}))
	require.NoError(t, reg.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("gate").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(id))
	require.NoError(t, err)

	select {
	case <-entered:
	case <-time.After(time.Second * 10):
		require.Fail(t, "the activity never started")
	}

	// The result must reach a workflow actor with nothing cached, so its
	// admission reads the instance from the store.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, a := range l.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors {
			if strings.HasSuffix(a.Type, ".workflow") {
				assert.Zero(c, a.Count)
			}
		}
	}, time.Second*10, time.Millisecond*10)

	lagged := make(chan struct{})
	l.store.ArmGetEmpty(id+"||metadata", 1, lagged)
	close(release)

	select {
	case <-lagged:
	case <-time.After(time.Second * 3):
		require.Fail(t, "the activity result was never answered from a lagging read")
	}

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "a refused result must not strand a running workflow")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"rerun"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(2), runs.Load(), "the janitor re-dispatch must re-run the activity once")
}
