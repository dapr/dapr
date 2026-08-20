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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
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
	suite.Register(new(residencychurn))
}

// residencychurn drives many short workflows under constant deactivation
// pressure and requires prompt completion without janitor recovery, a drained
// scheduler, and reloadable terminal state.
type residencychurn struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (r *residencychurn) Setup(t *testing.T) []framework.Option {
	app := app.New(t)
	r.place = placement.New(t)
	r.scheduler = procscheduler.New(t)
	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.place, app, r.daprd),
	}
}

func (r *residencychurn) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("ChurnSeq", func(c *task.WorkflowContext) (any, error) {
		var out string
		for i := range 3 {
			if err := c.CallActivity("Step",
				task.WithActivityInput(fmt.Sprintf("s%d", i)),
			).Await(&out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("Step", func(c task.ActivityContext) (any, error) {
		var in string
		if err := c.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "-done", nil
	}))

	cl := client.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	const total = 60
	ids := make([]api.InstanceID, total)

	var wg sync.WaitGroup
	errs := make([]error, total)
	for i := range total {
		id, err := cl.ScheduleNewWorkflow(ctx, "ChurnSeq",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("churn-%03d", i))))
		require.NoError(t, err)
		ids[i] = id

		wg.Add(1)
		go func(idx int, wid api.InstanceID) {
			defer wg.Done()
			wctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			meta, werr := cl.WaitForWorkflowCompletion(wctx, wid)
			if werr != nil {
				errs[idx] = werr
				return
			}
			if !strings.Contains(meta.GetRuntimeStatus().String(), "COMPLETED") {
				errs[idx] = fmt.Errorf("workflow %s ended %s", wid, meta.GetRuntimeStatus())
			}
		}(i, id)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "workflow churn-%03d must complete promptly without janitor recovery", i)
	}

	for _, i := range []int{0, total / 2, total - 1} {
		meta, err := cl.FetchWorkflowMetadata(ctx, ids[i])
		require.NoError(t, err)
		require.NotNil(t, meta)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var leaked int
		for _, key := range r.scheduler.ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.Contains(key, "new-event") && !strings.Contains(key, "janitor") {
				leaked++
			}
			if strings.Contains(key, "run-activity") || strings.Contains(key, "activity-result") {
				leaked++
			}
		}
		assert.Zero(c, leaked, "scheduler must drain to zero workflow one-shot jobs")
	}, 30*time.Second, 10*time.Millisecond)
}
