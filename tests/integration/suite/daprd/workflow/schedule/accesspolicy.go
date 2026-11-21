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

package schedule

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(accesspolicy))
}

// accesspolicy checks the target app's WorkflowAccessPolicy gates the runs
// another app's schedule starts there: an allowed run starts, a refused one
// is dropped while the schedule carries on, and reading the last run needs
// `get`. The target's own schedules are not gated.
type accesspolicy struct {
	workflow *workflow.Workflow
}

func (a *accesspolicy) Setup(t *testing.T) []framework.Option {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: schedule-target
scopes:
- schedule-target
spec:
  rules:
  - callers:
    - appID: schedule-caller
    workflows:
    - name: allowed
      operations: [schedule]
`), 0o600))

	a.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0, daprd.WithAppID("schedule-caller")),
		workflow.WithDaprdOptions(1, daprd.WithAppID("schedule-target"), daprd.WithResourcesDir(dir)),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *accesspolicy) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	noop := func(*task.WorkflowContext) (any, error) { return nil, nil }
	a.workflow.RegistryN(1).AddWorkflowN("allowed", noop)
	a.workflow.RegistryN(1).AddWorkflowN("refused", noop)
	caller := a.workflow.BackendClientN(t, ctx, 0)
	target := a.workflow.BackendClientN(t, ctx, 1)

	for _, name := range []string{"allowed", "refused"} {
		_, err := caller.CreateWorkflowSchedule(ctx, name, name, "@every 1s", api.WithScheduleTargetAppID("schedule-target"))
		require.NoError(t, err)
	}
	_, err := target.CreateWorkflowSchedule(ctx, "local", "refused", "@every 1s")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(wf.ScheduleRuns(c, ctx, target, "allowed")), 2)
		assert.GreaterOrEqual(c, len(wf.ScheduleRuns(c, ctx, target, "local")), 2, "the policy only gates other apps")

		resp, gerr := caller.GetWorkflowSchedule(ctx, "refused")
		if assert.NoError(c, gerr) {
			st := resp.GetSchedule().GetStatus()
			assert.Equal(c, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE, st.GetState())
			assert.GreaterOrEqual(c, st.GetRunCount(), uint32(2), "a refused run still counts")
		}
	}, 20*time.Second, 10*time.Millisecond)
	assert.Empty(t, wf.ScheduleRuns(t, ctx, target, "refused"), "a refused run never starts")

	// Reading the last run is a `get` on the target, which the policy does not
	// grant, so only its instance ID is known.
	resp, err := caller.GetWorkflowSchedule(ctx, "allowed")
	require.NoError(t, err)
	run := resp.GetSchedule().GetStatus().GetLastRun()
	assert.NotEmpty(t, run.GetInstanceId())
	assert.Nil(t, run.RuntimeStatus)
}
