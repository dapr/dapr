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

package payloadsize

import (
	"context"
	"strings"
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
	suite.Register(new(restartresumefold))
}

// restartresumefold verifies a PAYLOAD_SIZE_EXCEEDED stall whose completions
// arrived through the WorkflowsFastPath fold path recovers after daprd
// restarts with a larger --max-body-size: the stalling turn persists the
// folded completions to the durable inbox (a nacked fold dies with the
// sender's process), and the janitor re-drives the turn once the limit allows.
type restartresumefold struct {
	workflow *workflow.Workflow
}

func (r *restartresumefold) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("1Mi"),
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			// Post-restart recovery of the stalled instance rides the janitor; the
			// default 20s period eats the 45s case budget.
			daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*500),
		),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restartresumefold) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const chunkSize = 250 * 1024
	const numActivities = 4
	chunk := strings.Repeat("x", chunkSize)

	r.workflow.Registry().AddWorkflowN("growing-history", func(wctx *task.WorkflowContext) (any, error) {
		for range numActivities {
			var out string
			if err := wctx.CallActivity("emit-chunk").Await(&out); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	r.workflow.Registry().AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
		return chunk, nil
	})

	id := api.InstanceID("workflow-resume-fold")
	preClient := r.workflow.BackendClient(t, ctx)
	_, err := preClient.ScheduleNewWorkflow(ctx, "growing-history", api.WithInstanceID(id))
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, preClient, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, preClient, id)
	require.NotNil(t, stalled)
	require.Equal(t, protos.StalledReason_PAYLOAD_SIZE_EXCEEDED, stalled.GetExecutionStalled().GetReason())

	// Restarting immediately kills the folded completion's sender with the
	// process: only the durable inbox row written by the stalling turn can
	// resolve the outstanding activity afterwards.
	r.workflow.Dapr().ReplaceArg(t, "max-body-size", "16Mi")
	r.workflow.Dapr().Restart(t, ctx)

	postClient := r.workflow.BackendClient(t, ctx)

	md, err := postClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED.String(), md.RuntimeStatus.String())
}
