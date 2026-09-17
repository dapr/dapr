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

package activitydispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retry))
}

// retry asserts that orchestrator-driven activity retries work under pull:
// each attempt is a new activity task dispatched into a free slot, failures
// release the slot, and the workflow sees the final result.
type retry struct {
	workflow *workflow.Workflow
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	const appID = "pull-retry"
	cfg := pullConfig("pullretry", 1)
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var attempts atomic.Int64
	for i := range 2 {
		r.workflow.RegistryN(i).AddWorkflowN("retrying", func(ctx *task.WorkflowContext) (any, error) {
			var out string
			err := ctx.CallActivity("flaky", task.WithActivityRetryPolicy(&task.RetryPolicy{
				MaxAttempts:          3,
				InitialRetryInterval: 10 * time.Millisecond,
			})).Await(&out)
			if err != nil {
				return nil, err
			}
			return out, nil
		})
		r.workflow.RegistryN(i).AddActivityN("flaky", func(ctx task.ActivityContext) (any, error) {
			if attempts.Add(1) < 3 {
				return nil, errors.New("not yet")
			}
			return "done", nil
		})
	}

	client := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

	id, err := client.ScheduleNewWorkflow(ctx, "retrying", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(3), attempts.Load())

	// Failed attempts released their slots: the backlog is empty afterwards.
	assert.InDelta(t, 0.0, r.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
		"dapr_scheduler_workflow_activity_backlog", "app_id:pull-retry"), 0)
}
