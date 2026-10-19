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

// Package standby tests the WorkflowsStandbyHosts preview: while no daprd of
// an app has a workflow worker connected, its daprds host the workflow actor
// type as standby hosts, so workflow operations take their normal path and
// the instance runs once a worker connects.
package standby

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
// workflow API. Both starts return at once with the instance pending, a
// second start of the same ID fails synchronously, and both run once a
// worker connects to the other replica.
type start struct {
	workflow *workflow.Workflow
}

func (s *start) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStandbyHosts(),
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

	// Without standby hosts, both starts block until their deadline.
	sctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	mgmt := s.workflow.ManagementClientN(t, ctx, 0)

	// The standby host is placed once the placement table arrives.
	var sdkID api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		sdkID, err = mgmt.ScheduleNewWorkflow(sctx, "wf",
			api.WithInstanceID("standby-sdk"),
			api.WithInput("from-sdk"),
		)
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*50)

	resp, err := s.workflow.GRPCClientN(t, ctx, 0).StartWorkflowBeta1(sctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        "standby-api",
		Input:             []byte(`"from-api"`),
	})
	require.NoError(t, err)
	apiID := api.InstanceID(resp.GetInstanceId())

	// The instances exist and are pending, read through the normal path.
	for _, id := range []api.InstanceID{sdkID, apiID} {
		meta, merr := mgmt.FetchWorkflowMetadata(sctx, id)
		require.NoError(t, merr)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())
	}

	// A duplicate start fails synchronously, as it does with a worker.
	_, err = mgmt.ScheduleNewWorkflow(sctx, "wf",
		api.WithInstanceID(sdkID),
		api.WithInput("again"),
	)
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	s.workflow.ConnectWorkerN(t, ctx, 1, s.workflow.RegistryN(1))

	for id, out := range map[api.InstanceID]string{sdkID: `"from-sdk"`, apiID: `"from-api"`} {
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, out, meta.GetOutput().GetValue())
	}
}
