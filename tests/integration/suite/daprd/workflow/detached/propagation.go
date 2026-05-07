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

package detached

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(propagation))
}

// propagation locks in the trust-boundary guarantee for detached workflows:
// a spawned instance must NEVER see its caller's propagated history. The
// caller may have arbitrary signed events in its own history (activity
// scheduling, completed activities, etc.); none of that may leak through to
// the spawn. ScheduleNewWorkflow does not currently expose any option to
// request propagation, but we still assert the negative end-to-end so any
// future change that wires propagation in by accident is caught.
type propagation struct {
	workflow *workflow.Workflow
}

func (p *propagation) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *propagation) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-no-propagation"
	spawnedSawHistory := make(chan bool, 1)

	// Caller calls a real activity so its history has substantive events
	// (TaskScheduled / TaskCompleted) that would be visible if propagation
	// were leaking.
	p.workflow.Registry().AddActivityN("CallerAct", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})
	p.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("CallerAct").Await(nil); err != nil {
			return nil, err
		}
		_, err := ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID))
		return nil, err
	})
	p.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		// Report what the spawn sees as propagated history. Anything other
		// than nil is a leak.
		spawnedSawHistory <- ctx.GetPropagatedHistory() != nil
		return "ok", nil
	})

	client := p.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	parentMeta, err := client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())

	// (1) The spawned workflow itself observed no propagated history. By
	// the time WaitForWorkflowCompletion has returned the value is already
	// in the buffered channel, so the timeout is a guard rail, not a wait.
	select {
	case sawHistory := <-spawnedSawHistory:
		assert.False(t, sawHistory,
			"spawned workflow must observe nil from GetPropagatedHistory(); detached spawns must not inherit caller history")
	case <-time.After(5 * time.Second):
		t.Fatal("spawned workflow body never reported its propagated-history view")
	}

	// (2) Belt-and-braces: the spawned instance has zero propagated-history
	// rows in the state store. If anything wrote one, the assertion above
	// could still pass on a stale in-memory view; the SQL check is
	// authoritative.
	db := p.workflow.DB().GetConnection(t)
	tableName := p.workflow.DB().TableName()
	var count int
	require.NoError(t,
		db.QueryRowContext(ctx,
			//nolint:gosec
			"SELECT COUNT(*) FROM "+tableName+" WHERE key LIKE ? AND key LIKE '%propagated-history'",
			"%"+strings.ToLower(spawnedInstanceID)+"%",
		).Scan(&count))
	assert.Equal(t, 0, count,
		"detached spawn must have no propagated-history rows in the state store")
}
