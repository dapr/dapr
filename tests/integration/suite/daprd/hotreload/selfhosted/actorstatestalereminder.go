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

package selfhosted

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(actorstatestalereminder))
}

type actorstatestalereminder struct {
	daprd   *daprd.Daprd
	sched   *scheduler.Scheduler
	logline *logline.LogLine
	resDir  string
}

func (a *actorstatestalereminder) Setup(t *testing.T) []framework.Option {
	a.sched = scheduler.New(t)
	place := placement.New(t)

	a.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"No workflow state found for actor 'stale-instance', terminating execution",
		),
	)

	a.resDir = t.TempDir()

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithScheduler(a.sched),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(exec.WithStdout(a.logline.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(a.sched, place, a.logline, a.daprd),
	}
}

func (a *actorstatestalereminder) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	// Connect a workflow worker before any actor state store exists.
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	}))
	require.NoError(t, reg.AddWorkflowN("SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	}))
	wfClient := dtclient.NewTaskHubGrpcClient(a.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, wfClient.StartWorkItemListener(ctx, reg))

	// Simulate the stale durable reminder left behind in the scheduler by a
	// previous incarnation of this app which did have an actor state store.
	_, err := a.sched.Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "new-event-repro",
		Job:  &schedulerv1pb.Job{DueTime: new("0s")},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.daprd.AppID(),
			Namespace: a.daprd.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Id:   "stale-instance",
						Type: "dapr.internal." + a.daprd.Namespace() + "." + a.daprd.AppID() + ".workflow",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := a.sched.MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_jobs_undelivered_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.GreaterOrEqual(c, total["type=actor"], 1.0)
	}, time.Second*20, time.Millisecond*10)

	require.NotNil(t, a.daprd.GetMetadata(t, ctx))

	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

	assert.Eventually(t, a.logline.FoundAll, time.Second*30, time.Millisecond*10)

	id, err := wfClient.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"), api.WithInstanceID("afterstoreadd"))
	require.NoError(t, err)
	meta, err := wfClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
}
