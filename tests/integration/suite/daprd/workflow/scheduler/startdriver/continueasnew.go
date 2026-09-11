/*
Copyright 2025 The Dapr Authors
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

package startdriver

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew pins the one-driver rule for the start a ContinueAsNew
// reseeds: the new generation is driven locally while its durable start
// reminder stays a dormant backstop, not due while the turn is held.
type continueasnew struct {
	workflow *workflow.Workflow
	called   atomic.Int64
	waitCh   chan struct{}
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.waitCh = make(chan struct{})
	c.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "5m",
		))),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	c.workflow.Registry().AddWorkflowN("gen", func(wctx *task.WorkflowContext) (any, error) {
		var generation int
		if err := wctx.GetInput(&generation); err != nil {
			return nil, err
		}
		if generation == 0 {
			wctx.ContinueAsNew(1)
			return nil, nil
		}
		c.called.Add(1)
		<-c.waitCh
		return "done", nil
	})
	cl := c.workflow.BackendClient(t, ctx)

	triggered := func() int {
		return int(c.workflow.Scheduler().Metrics(t, ctx).All()["dapr_scheduler_jobs_triggered_total"])
	}

	// A start time keeps the create from waiting on the first commit, which
	// the body holds.
	id, err := cl.ScheduleNewWorkflow(ctx, "gen", api.WithInput(0), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// The second generation's start turn is held in the body.
	require.Eventually(t, func() bool { return c.called.Load() == 1 }, time.Second*10, time.Millisecond*10)

	appID := c.workflow.Dapr().AppID()
	var sched schedulerv1pb.SchedulerClient
	if c.workflow.Signing() {
		sched = c.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		sched = c.workflow.Scheduler().Client(t, ctx)
	}
	jobs, err := sched.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Type: c.workflow.WorkflowActorType(0), Id: string(id)},
				},
			},
		},
	})
	require.NoError(t, err)
	var startDue time.Time
	for _, job := range jobs.GetJobs() {
		if strings.HasPrefix(job.GetName(), "start-es-") {
			startDue, err = time.Parse(time.RFC3339Nano, job.GetJob().GetDueTime())
			require.NoError(t, err)
		}
	}
	require.False(t, startDue.IsZero(), "the reseeded start must have its backstop")
	assert.True(t, startDue.After(time.Now()),
		"reseeded start reminder due %s while the local drive holds the turn: a second driver", startDue)
	assert.Never(t, func() bool { return triggered() > 0 }, time.Second*2, time.Millisecond*10)

	close(c.waitCh)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(1), c.called.Load())
	assert.Zero(t, triggered())
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Zero(col, c.workflow.Scheduler().JobKeyCount(t, ctx, "||start-es-"))
	}, time.Second*10, time.Millisecond*10)
}
