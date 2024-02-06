/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/client"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(workflow))
}

// workflow tests daprd metrics for workflows
type workflow struct {
	base
}

func (m *workflow) Setup(t *testing.T) []framework.Option {
	return m.testSetup(t)
}

func (m *workflow) Run(t *testing.T, ctx context.Context) {
	m.beforeRun(t, ctx)

	// Register workflow
	r := task.NewTaskRegistry()
	r.AddActivityN("activity_success", func(ctx task.ActivityContext) (any, error) {
		return "success", nil
	})
	r.AddActivityN("activity_failure", func(ctx task.ActivityContext) (any, error) {
		return nil, fmt.Errorf("failure")
	})
	r.AddOrchestratorN("workflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		activityName := input
		err := ctx.CallActivity(activityName).Await(nil)
		if err != nil {
			return nil, err
		}
		return nil, nil
	})
	taskhubClient := client.NewTaskHubGrpcClient(m.grpcConn, backend.DefaultLogger())
	taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
	taskhubClient.StartWorkItemListener(taskhubCtx, r)
	defer cancelTaskhub()

	t.Run("successful workflow execution", func(t *testing.T) {
		id, err := taskhubClient.ScheduleNewOrchestration(ctx, "workflow", api.WithInput("activity_success"))
		require.NoError(t, err)
		timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
		t.Cleanup(cancelTimeout)
		metadata, err := taskhubClient.WaitForOrchestrationCompletion(timeoutCtx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, metadata.IsComplete())

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 1, int(metrics["dapr_runtime_workflow_operation_count|app_id:myapp|namespace:|operation:create_workflow|status:success"]))
		assert.Equal(t, 1, int(metrics["dapr_runtime_workflow_execution_count|app_id:myapp|namespace:|status:success|workflow_name:workflow"]))
		assert.Equal(t, 1, int(metrics["dapr_runtime_workflow_activity_execution_count|activity_name:activity_success|app_id:myapp|namespace:|status:success"]))
		assert.GreaterOrEqual(t, 1, int(metrics["dapr_runtime_workflow_execution_latency|app_id:myapp|namespace:|status:success|workflow_name:workflow"]))
		assert.GreaterOrEqual(t, 1, int(metrics["dapr_runtime_workflow_scheduling_latency|app_id:myapp|namespace:|workflow_name:workflow"]))
	})
	t.Run("failed workflow execution", func(t *testing.T) {
		id, err := taskhubClient.ScheduleNewOrchestration(ctx, "workflow", api.WithInput("activity_failure"))
		require.NoError(t, err)
		timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
		t.Cleanup(cancelTimeout)
		metadata, err := taskhubClient.WaitForOrchestrationCompletion(timeoutCtx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, metadata.IsComplete())

		// Verify metrics
		metrics := m.getMetrics(t, ctx)
		assert.Equal(t, 2, int(metrics["dapr_runtime_workflow_operation_count|app_id:myapp|namespace:|operation:create_workflow|status:success"]))
		assert.Equal(t, 1, int(metrics["dapr_runtime_workflow_execution_count|app_id:myapp|namespace:|status:failed|workflow_name:workflow"]))
		assert.Equal(t, 1, int(metrics["dapr_runtime_workflow_activity_execution_count|activity_name:activity_failure|app_id:myapp|namespace:|status:failed"]))
	})
}
