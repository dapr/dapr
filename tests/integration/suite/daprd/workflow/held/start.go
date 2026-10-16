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

package held

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(start))
}

// start schedules workflows on a daprd while no replica of the app has a
// worker connected, through both the SDK's durabletask API and Dapr's own
// workflow API. Both starts return at once and are held in the scheduler,
// and they run once a worker connects to the other replica.
type start struct {
	workflow *workflow.Workflow
}

func (s *start) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithHeldOperations(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *start) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, s.workflow.RegistryN(1).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	}))

	// Without held operations, both starts block until their deadline.
	sctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	mgmt := s.workflow.ManagementClientN(t, ctx, 0)
	sdkID, err := mgmt.ScheduleNewWorkflow(sctx, "wf",
		api.WithInstanceID("held-sdk"),
		api.WithInput("from-sdk"),
	)
	require.NoError(t, err)

	resp, err := s.workflow.GRPCClientN(t, ctx, 0).StartWorkflowBeta1(sctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        "held-api",
		Input:             []byte(`"from-api"`),
	})
	require.NoError(t, err)
	apiID := api.InstanceID(resp.GetInstanceId())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, heldKeys(t, ctx, s.workflow, sdkID), 1)
		assert.Len(c, heldKeys(t, ctx, s.workflow, apiID), 1)
	}, time.Second*10, time.Millisecond*10)

	s.workflow.ConnectWorkerN(t, ctx, 1, s.workflow.RegistryN(1))

	for id, out := range map[api.InstanceID]string{sdkID: `"from-sdk"`, apiID: `"from-api"`} {
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, out, meta.GetOutput().GetValue())
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, heldKeys(t, ctx, s.workflow, sdkID))
		assert.Empty(c, heldKeys(t, ctx, s.workflow, apiID))
	}, time.Second*10, time.Millisecond*10)
}
