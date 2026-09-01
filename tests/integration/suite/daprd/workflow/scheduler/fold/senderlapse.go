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

package fold

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(senderlapse))
}

type senderlapse struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (f *senderlapse) Setup(t *testing.T) []framework.Option {
	f.place = placement.New(t)
	f.scheduler = procscheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	f.daprd = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithSchedulerAddresses(f.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			"DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES", "100",
			"DAPR_WORKFLOW_FOLD_WAIT_TIMEOUT", "200ms",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(f.place, f.scheduler, db, f.daprd),
	}
}

func (f *senderlapse) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.place.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Seq", func(c *task.WorkflowContext) (any, error) {
		var out string
		for range 3 {
			if err := c.CallActivity("Echo", task.WithActivityInput("x")).Await(&out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}))
	require.NoError(t, registry.AddActivityN("Echo", func(task.ActivityContext) (any, error) {
		return "ok", nil
	}))

	cl := client.NewTaskHubGrpcClient(f.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	resp, err := f.daprd.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Seq",
	})
	require.NoError(t, err)

	wctx, cancel := context.WithTimeout(ctx, time.Second*45)
	defer cancel()
	metadata, err := cl.WaitForWorkflowCompletion(wctx, api.InstanceID(resp.GetInstanceId()))
	require.NoError(t, err, "workflow stranded despite sender retries and janitor recovery")
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"ok"`, metadata.GetOutput().GetValue())

	folded := f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:folded")
	nacked := f.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_completions_fold_count", "status:fold_nacked")
	assert.GreaterOrEqual(t, folded, float64(2),
		"non-terminal captive entries must fold: any committing turn takes them first")
	assert.LessOrEqual(t, folded, float64(3), "folded is commit-attributed: at most one record per completion")
	assert.GreaterOrEqual(t, nacked, float64(3),
		"every completion's first submit must lapse at the shortened fold wait")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors := f.scheduler.JobKeyCount(t, ctx, "new-event-janitor")
		newEvents := f.scheduler.JobKeyCount(t, ctx, "new-event") - janitors
		assert.Zero(c, janitors, "the janitor must be deleted at the terminal turn")
		assert.Zero(c, newEvents)
		assert.Zero(c, f.scheduler.JobKeyCount(t, ctx, "run-activity"))
	}, time.Second*60, time.Millisecond*50)
}
