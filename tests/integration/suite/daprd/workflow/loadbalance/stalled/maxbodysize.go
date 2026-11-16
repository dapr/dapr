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

package stalled

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(maxbodysize))
}

// maxbodysize verifies a workflow stalls with PAYLOAD_SIZE_EXCEEDED in a
// clustered deployment and completes after the cluster is rolled to a larger
// --max-body-size.
type maxbodysize struct {
	workflow *workflow.Workflow
}

func (m *maxbodysize) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.NewClustered(t, 2,
		daprd.WithMaxBodySize("1Mi"),
		daprd.WithWorkflowJanitorPeriod(t, time.Millisecond*500),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *maxbodysize) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	// 4 chunks of 250 KiB cross the 95% stall threshold of the 1 MiB limit.
	const chunkSize = 250 * 1024
	const numActivities = 4
	chunk := strings.Repeat("x", chunkSize)

	newWorkers := func(t *testing.T, ctx context.Context) []*client.TaskHubGrpcClient {
		t.Helper()
		clients := make([]*client.TaskHubGrpcClient, 2)
		for i := range clients {
			registry := task.NewTaskRegistry()
			require.NoError(t, registry.AddWorkflowN("growing-history", func(wctx *task.WorkflowContext) (any, error) {
				for range numActivities {
					var out string
					if err := wctx.CallActivity("emit-chunk").Await(&out); err != nil {
						return nil, err
					}
				}
				return nil, nil
			}))
			require.NoError(t, registry.AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
				return chunk, nil
			}))
			clients[i] = client.NewTaskHubGrpcClient(m.workflow.DaprN(i).GRPCConn(t, ctx), logger.New(t))
			require.NoError(t, clients[i].StartWorkItemListener(ctx, registry))
		}
		return clients
	}

	clients := newWorkers(t, ctx)
	id, err := clients[0].ScheduleNewWorkflow(ctx, "growing-history")
	require.NoError(t, err)

	for i := range clients {
		wf.WaitForRuntimeStatus(t, ctx, clients[i], id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
		lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, clients[i], id)
		require.NotNil(t, lastEvent)
		require.Equal(t, protos.StalledReason_PAYLOAD_SIZE_EXCEEDED, lastEvent.GetExecutionStalled().GetReason())
		require.Contains(t, lastEvent.GetExecutionStalled().GetDescription(), "exceeds")
	}

	for i := range 2 {
		m.workflow.DaprN(i).ReplaceArg(t, "max-body-size", "16Mi")
		m.workflow.DaprN(i).Restart(t, ctx)
	}
	m.workflow.WaitUntilRunning(t, ctx)

	clients = newWorkers(t, ctx)
	for i := range clients {
		wf.WaitForRuntimeStatus(t, ctx, clients[i], id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	}
}
