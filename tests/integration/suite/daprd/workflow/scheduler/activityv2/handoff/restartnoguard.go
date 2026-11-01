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

package handoff

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restartnoguard))
}

// restartnoguard gracefully restarts the hosting daprd mid-activity (the
// HaltAll path): the workflow must recover through the normal janitor
// re-dispatch, and no execution-claim record may ever be written, pinning
// the deliberate no-guard-on-shutdown decision (a record for an execution
// dying with its process would only stall the new owner).
type restartnoguard struct {
	workflow *workflow.Workflow
}

func (a *restartnoguard) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t, workflow.WithDaprdOptions(0,
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithWorkflowJanitorPeriod(t, time.Second*2),
	))

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *restartnoguard) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// Audit triggers record every execution-claim write inside the writer's
	// own transaction, so a record written and deleted again during the
	// shutdown window cannot slip between poll samples.
	claimWrites := wf.AuditClaimWrites(t, ctx, a.workflow.DB())

	var restarted atomic.Bool
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)

	require.NoError(t, a.workflow.Registry().AddWorkflowN("RestartNoGuard", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("Slow", task.WithActivityInput("Dapr")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, a.workflow.Registry().AddActivityN("Slow", func(task.ActivityContext) (any, error) {
		if restarted.Load() {
			return "recovered", nil
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil, nil
	}))
	a.workflow.BackendClient(t, ctx)

	resp, err := a.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "RestartNoGuard",
	})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start executing")
	}

	// Graceful restart mid-execution: HaltAll runs with the claim unsettled,
	// and must spawn no guard.
	restarted.Store(true)
	a.workflow.Dapr().RestartGraceful(t, ctx)
	a.workflow.WaitUntilRunning(t, ctx)

	client2 := a.workflow.BackendClient(t, ctx)
	metadata, err := client2.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"recovered"`, metadata.GetOutput().GetValue())

	assert.Zero(t, claimWrites(),
		"a graceful shutdown must never write an execution-claim record")
	assert.Zero(t, wf.CountClaimRecords(t, ctx, a.workflow.DB()))
}
