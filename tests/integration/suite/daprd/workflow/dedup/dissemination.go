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

package dedup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(dissemination))
}

type dissemination struct {
	workflow *workflow.Workflow
}

func (d *dissemination) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithPlacementOptions(placement.WithDisseminateTimeout(time.Second*7)),
	)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *dissemination) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32
	var startOnce sync.Once
	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})

	require.NoError(t, d.workflow.Registry().AddWorkflowN("dedup-dissemination", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("slow").Await(nil)
	}))
	require.NoError(t, d.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		startOnce.Do(func() { close(activityStarted) })
		select {
		case <-releaseActivity:
			return nil, nil
		case <-ctx.Context().Done():
			return nil, ctx.Context().Err()
		}
	}))

	cl := d.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-dissemination")
	require.NoError(t, err)

	select {
	case <-activityStarted:
	case <-time.After(20 * time.Second):
		require.Fail(t, "activity body never started")
	}

	startVersion := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	pclient := d.workflow.Placement().Client(t, ctx)
	blocker, err := pclient.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Send(&v1pb.Host{
		Name:      "blocker",
		Port:      9999,
		Entities:  []string{"someactor"},
		Id:        "blocker",
		Namespace: "default",
	}))
	go func() {
		for {
			if _, rerr := blocker.Recv(); rerr != nil {
				return
			}
		}
	}()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := d.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		assert.Greater(c, table.Version, startVersion, "placement table version must advance after blocker joins")
	}, 15*time.Second, 10*time.Millisecond)

	time.Sleep(time.Second * 2)
	close(releaseActivity)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.Equal(t, int32(1), activityCalls.Load(),
		"activity body must run exactly once even when dissemination fires mid-execution")
}
