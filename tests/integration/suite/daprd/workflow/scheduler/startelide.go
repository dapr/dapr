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

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(startelide))
}

// startelide pins the fast-path start-trigger elision: the pending start
// one-shot is deleted without ever firing once the first turn commits, while
// a delayed start keeps its entry until the due time.
type startelide struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (s *startelide) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	s.place = placement.New(t)
	s.scheduler = procscheduler.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "5m",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.place, app, s.daprd),
	}
}

func (s *startelide) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("Park", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.WaitForSingleEvent("go", time.Minute*3).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddWorkflowN("Quick", func(c *task.WorkflowContext) (any, error) {
		return "done", nil
	}))

	backendClient := client.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, reg))

	triggered := func() int {
		return int(s.scheduler.Metrics(t, ctx).All()["dapr_scheduler_jobs_triggered_total"])
	}

	resp, err := s.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Park",
		Input:             []byte(`"x"`),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, s.scheduler.JobKeyCount(t, ctx, "||start-es-"))
	}, time.Second*20, time.Millisecond*50)
	meta, err := backendClient.FetchWorkflowMetadata(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	require.False(t, api.WorkflowMetadataIsComplete(meta),
		"the workflow must still be parked when its start entry disappears")
	assert.Zero(t, triggered(),
		"the start one-shot must be elided without a scheduler trigger cycle")

	_, err = s.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
		EventData:         []byte(`"y"`),
	})
	require.NoError(t, err)
	meta, err = backendClient.WaitForWorkflowCompletion(ctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))

	// A delayed start must keep its entry until the due time: the elision
	// only runs after the first turn commits.
	id, err := backendClient.ScheduleNewWorkflow(ctx, "Quick",
		api.WithInstanceID("delayed"),
		api.WithStartTime(time.Now().Add(time.Second*3)))
	require.NoError(t, err)
	assert.Equal(t, 1, s.scheduler.JobKeyCount(t, ctx, "||start-es-"),
		"the delayed start entry must survive until its due time")
	meta, err = backendClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, s.scheduler.JobKeyCount(t, ctx, "||start-es-"))
	}, time.Second*10, time.Millisecond*50)
}
