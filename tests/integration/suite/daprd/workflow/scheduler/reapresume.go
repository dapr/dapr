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
	"strings"
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
	suite.Register(new(reapresume))
}

// reapresume parks a workflow on an external event, waits for the idle reaper
// to release its actor, then raises the event and requires the workflow to
// resume and complete.
type reapresume struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (r *reapresume) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	r.place = placement.New(t)
	r.scheduler = procscheduler.New(t)
	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "4s",
			"DAPR_WORKFLOW_REAPER_SCAN_INTERVAL", "500ms",
			"DAPR_WORKFLOW_REAPER_IDLE_TTL", "1s",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place, app, r.daprd),
	}
}

func (r *reapresume) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("ParkThenResume", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.WaitForSingleEvent("go", time.Minute).Await(&out); err != nil {
			return nil, err
		}
		return "resumed-" + out, nil
	}))

	backendClient := client.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, backendClient.StartWorkItemListener(ctx, reg))

	resp, err := r.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "ParkThenResume",
		Input:             []byte(`"x"`),
	})
	require.NoError(t, err)

	workflowActors := func() int {
		for _, a := range r.daprd.GetMetadata(t, ctx).ActorRuntime.ActiveActors {
			if strings.HasSuffix(a.Type, ".workflow") {
				return a.Count
			}
		}
		return 0
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, workflowActors())
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, workflowActors())
	}, time.Second*20, time.Millisecond*10)

	_, err = r.daprd.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		InstanceId:        resp.GetInstanceId(),
		WorkflowComponent: "dapr",
		EventName:         "go",
		EventData:         []byte(`"y"`),
	})
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	metadata, err := backendClient.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"resumed-y"`, metadata.GetOutput().GetValue())
}
