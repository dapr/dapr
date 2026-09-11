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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
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
	suite.Register(new(startonedriver))
}

// startonedriver pins the one-driver rule for the start event under the fast
// path: while the local drive holds the first turn, the scheduler must not
// fire a second driver for the same start. The durable start reminder is a
// dormant backstop, due a janitor period out and elided once the turn
// commits; a due-now backstop fired beside the local drive and ran the turn
// twice whenever the first attempt aborted.
type startonedriver struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
	called    atomic.Int64
	waitCh    chan struct{}
}

func (s *startonedriver) Setup(t *testing.T) []framework.Option {
	s.waitCh = make(chan struct{})
	app := app.New(t)
	s.place = placement.New(t)
	s.scheduler = procscheduler.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		// The janitor is a legitimate second fire; keep it out of the window.
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "5m",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.place, app, s.daprd),
	}
}

func (s *startonedriver) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("hold", func(*task.WorkflowContext) (any, error) {
		s.called.Add(1)
		<-s.waitCh
		return "held", nil
	}))
	cl := client.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	triggered := func() int {
		return int(s.scheduler.Metrics(t, ctx).All()["dapr_scheduler_jobs_triggered_total"])
	}

	id, err := cl.ScheduleNewWorkflow(ctx, "hold", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// The local drive holds the first turn in the workflow body.
	require.Eventually(t, func() bool { return s.called.Load() == 1 }, time.Second*10, time.Millisecond*10)

	// With the turn held, the durable start reminder must be a dormant
	// backstop: not due while the local drive owns the turn.
	actorType := "dapr.internal.default." + s.daprd.AppID() + ".workflow"
	jobs, err := s.scheduler.Client(t, ctx).ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: s.daprd.AppID(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Type: actorType, Id: string(id)},
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
	require.False(t, startDue.IsZero(), "the start reminder must exist as the local drive's backstop")
	assert.True(t, startDue.After(time.Now()),
		"start reminder due %s while the local drive holds the turn: a second driver", startDue)

	assert.Never(t, func() bool { return triggered() > 0 }, time.Second*2, time.Millisecond*10,
		"the scheduler fired a second driver for a start the local drive already holds")

	close(s.waitCh)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(1), s.called.Load(), "one driver, one turn")
	assert.Zero(t, triggered())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, s.scheduler.JobKeyCount(t, ctx, "||start-es-"), "the committed start's backstop must be elided")
	}, time.Second*10, time.Millisecond*10)
}
