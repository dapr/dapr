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

package fallback

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(purgeescalation))
}

// purgeescalation purges two workflows while the local drives of their
// activities are escalating to the run-activity reminder: one by force while
// the daprd is a fallback host, one terminated and then purged normally once
// a worker is back. The escalations are held until the test releases them,
// after both purges, so each creates its reminder after the purge deleted the
// reminders. When that reminder fires it must be dropped, not run the
// activity once more for a workflow that no longer exists.
type purgeescalation struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
	// release is the file the held escalations wait for.
	release string
}

const purgeescalationHold = "TEST INJECTION: holding the run-activity escalation"

func (p *purgeescalation) Setup(t *testing.T) []framework.Option {
	p.logline = logline.New(t, logline.WithCaptureAll())
	p.release = filepath.Join(t.TempDir(), "release")
	p.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithStagedOperations(true),
		workflow.WithFallbackSettleN(t, 0),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ESCALATION_HOLD", p.release),
				exec.WithStdout(p.logline.Stdout()),
				exec.WithStderr(p.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(p.logline, p.workflow),
	}
}

func (p *purgeescalation) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var lock sync.Mutex
	runs := make(map[string]int)
	ranRuns := func(id string) int {
		lock.Lock()
		defer lock.Unlock()
		return runs[id]
	}

	require.NoError(t, p.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act", task.WithActivityInput(string(ctx.ID))).Await(nil)
	}))
	// The first run blocks until the worker disconnects; a later one is the
	// run a leftover reminder causes.
	require.NoError(t, p.workflow.Registry().AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		var id string
		if err := actx.GetInput(&id); err != nil {
			return nil, err
		}
		lock.Lock()
		runs[id]++
		first := runs[id] == 1
		lock.Unlock()
		if first {
			<-actx.Context().Done()
			return nil, actx.Context().Err()
		}
		return nil, nil
	}))

	worker := p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	mgmt := p.workflow.ManagementClient(t, ctx)

	forced, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("purgeescalation-force"))
	require.NoError(t, err)
	terminated, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("purgeescalation-terminate"))
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, ranRuns(string(forced)))
		assert.Equal(c, 1, ranRuns(string(terminated)))
	}, time.Second*10, time.Millisecond*10)

	// On the fallback host the drives of both activities fail and escalate.
	worker.Disconnect(t)
	p.workflow.WaitForNoConnectedWorkers(t, ctx)
	require.Eventually(t, func() bool {
		return p.logline.Count(purgeescalationHold) >= 2
	}, time.Second*10, time.Millisecond*10, "the activity drives did not escalate")

	require.NoError(t, mgmt.PurgeWorkflowState(ctx, forced, api.WithForcePurge(true)))

	// A fallback host stages the terminate until a worker connects.
	require.NoError(t, mgmt.TerminateWorkflow(ctx, terminated))
	p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	fworkflow.WaitForRuntimeStatus(t, ctx, mgmt, terminated, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)
	require.NoError(t, mgmt.PurgeWorkflowState(ctx, terminated))
	require.NoError(t, os.WriteFile(p.release, nil, 0o600))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, p.workflow.Dapr().Metrics(c, ctx).SumWithLabels(
			"dapr_runtime_workflow_local_activity_count", "status:purged_dropped"), float64(2))
	}, time.Second*20, time.Millisecond*10, "the escalated reminders were not dropped")

	jobs := p.workflow.Scheduler().JobKeysLookup(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, jobs(c, string(forced)))
		assert.Empty(c, jobs(c, string(terminated)))
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, 1, ranRuns(string(forced)), "the force purged workflow's activity ran again")
	assert.Equal(t, 1, ranRuns(string(terminated)), "the purged workflow's activity ran again")
}
