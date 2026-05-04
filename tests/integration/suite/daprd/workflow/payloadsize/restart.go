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
	suite.Register(new(restartresume))
}

// restartresume verifies that a workflow stalled with PAYLOAD_SIZE_EXCEEDED
// resumes and runs to completion after the operator restarts daprd with a
// larger --max-body-size.
type restartresume struct {
	workflow *workflow.Workflow
}

func (r *restartresume) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("1Mi"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restartresume) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const chunkSize = 250 * 1024
	const numActivities = 4
	chunk := strings.Repeat("x", chunkSize)

	require.NoError(t, r.workflow.Registry().AddOrchestratorN("growing-history", func(wctx *task.OrchestrationContext) (any, error) {
		for range numActivities {
			var out string
			if err := wctx.CallActivity("emit-chunk").Await(&out); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, r.workflow.Registry().AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
		return chunk, nil
	}))

	id := api.InstanceID("workflow-resume")
	preClient := r.workflow.BackendClient(t, ctx)
	_, err := preClient.ScheduleNewOrchestration(ctx, "growing-history", api.WithInstanceID(id))
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, preClient, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, preClient, id)
	require.NotNil(t, stalled)
	require.Equal(t, "PAYLOAD_SIZE_EXCEEDED", stalled.GetExecutionStalled().GetReason().String())

	// Restart daprd with a larger max-body-size. The new threshold is well above
	// the workflow's accumulated history, so the precheck no longer fires when
	// the workflow actor reactivates.
	r.workflow.Dapr().ReplaceArg(t, "max-body-size", "16Mi")
	r.workflow.Dapr().Restart(t, ctx)

	postClient := r.workflow.BackendClient(t, ctx)

	md, err := postClient.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED.String(), md.RuntimeStatus.String())
}
