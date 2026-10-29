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

// Package fallback tests the WorkflowsStagedOperations preview: while no daprd
// of an app has a workflow worker connected, its daprds host the workflow
// actor types as fallback hosts, so workflow operations take their normal
// path and the instance runs once a daprd with a worker owns it.
package fallback

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
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
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
		workflow.WithStagedOperations(),
		withSettle(t, 0),
		withSettle(t, 1),
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

	// A fallback host answers a start at once, with the instance pending;
	// without one, a start blocks until its deadline. Each such call has a
	// deadline of its own, well below the waits of this test, so a start
	// which blocks fails with DeadlineExceeded.
	const quick = time.Second * 5

	mgmt := s.workflow.ManagementClientN(t, ctx, 0)

	// The fallback hosts register once the placement tables have shown no
	// executor for the settle period.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, s.workflow.HostsActorType(c, ctx, 0, s.workflow.WorkflowActorType(0)))
		assert.True(c, s.workflow.HostsActorType(c, ctx, 1, s.workflow.WorkflowActorType(1)))
	}, time.Second*30, time.Millisecond*50)

	// Retried until the placement table which shows them reaches daprd 0.
	var sdkID api.InstanceID
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		sctx, cancel := context.WithTimeout(ctx, quick)
		defer cancel()
		var err error
		sdkID, err = mgmt.ScheduleNewWorkflow(sctx, "wf",
			api.WithInstanceID("fallback-sdk"),
			api.WithInput("from-sdk"),
		)
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*50)

	sctx, cancel := context.WithTimeout(ctx, quick)
	resp, err := s.workflow.GRPCClientN(t, ctx, 0).StartWorkflowBeta1(sctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        "fallback-api",
		Input:             []byte(`"from-api"`),
	})
	cancel()
	require.NoError(t, err)
	apiID := api.InstanceID(resp.GetInstanceId())

	// The instances exist and are pending, read through the normal path.
	for _, id := range []api.InstanceID{sdkID, apiID} {
		fctx, fcancel := context.WithTimeout(ctx, time.Second*30)
		meta, merr := mgmt.FetchWorkflowMetadata(fctx, id)
		fcancel()
		require.NoError(t, merr)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())
	}

	// A duplicate start fails synchronously, as it does with a worker.
	sctx, cancel = context.WithTimeout(ctx, quick)
	_, err = mgmt.ScheduleNewWorkflow(sctx, "wf",
		api.WithInstanceID(sdkID),
		api.WithInput("again"),
	)
	cancel()
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	// Each daprd counts the starts it served as a fallback host; the
	// duplicate failed, so it is not counted.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 2.0, stagedStarts(c, ctx, s.workflow, 0)+stagedStarts(c, ctx, s.workflow, 1), 0)
	}, time.Second*10, time.Millisecond*50)

	s.workflow.ConnectWorkerN(t, ctx, 1, s.workflow.RegistryN(1))

	for id, out := range map[api.InstanceID]string{sdkID: `"from-sdk"`, apiID: `"from-api"`} {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt, id)
		assert.Equal(t, out, meta.GetOutput().GetValue(), id)
	}
}
