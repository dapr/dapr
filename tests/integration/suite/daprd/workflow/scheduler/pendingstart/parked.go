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

package pendingstart

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(parked))
}

// parked: a status wait that parks on a pending start inside the
// grace must re-check it once the grace passes, rather than waiting for the
// next commit or actor deactivation. The stranded start here has no reminder
// and nothing else ever touches it.
type parked struct {
	workflow *workflow.Workflow
}

func (p *parked) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "2s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *parked) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("Parked", func(*task.WorkflowContext) (any, error) {
		return "ran", nil
	}))
	client := p.workflow.BackendClient(t, ctx)

	const instanceID = "pending-start-parked"
	p.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, []*protos.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "Parked",
				Input:            wrapperspb.String(`"Dapr"`),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
			},
		},
	}})

	// The wait registers while the start is fresh, so the read at
	// registration finds nothing overdue and the stream parks.
	started := time.Now()
	meta, err := client.WaitForWorkflowStart(ctx, api.InstanceID(instanceID))
	require.NoError(t, err, "the parked wait must be released by the re-check after the grace")
	require.NotEqual(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus())
	assert.Less(t, time.Since(started), 15*time.Second, "the re-check must fire at the grace, not at actor deactivation")

	meta, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(instanceID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Positive(t, p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven"))
}
