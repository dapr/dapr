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

package disk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(quota))
}

// quota fills the scheduler's embedded etcd past its storage quota before the
// workflow starts. Asserts the scheduler RPC and workflow-engine layers
// surface the error, then reclaims quota and runs the workflow to completion.
type quota struct {
	wf *workflow.Workflow
}

func (q *quota) Setup(t *testing.T) []framework.Option {
	q.wf = workflow.New(t,
		workflow.WithSchedulerOptions(scheduler.WithEtcdSpaceQuota("16Mi")),
		workflow.WithAddOrchestrator(t, "timerFlow", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CreateTimer(2 * time.Second).Await(nil)
		}),
	)
	return []framework.Option{framework.WithProcesses(q.wf)}
}

func (q *quota) Run(t *testing.T, ctx context.Context) {
	q.wf.WaitUntilRunning(t, ctx)

	sched := q.wf.Scheduler()
	sched.FillQuota(t, ctx, "quota-test/")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		alarms, err := sched.ETCDClient(t, ctx).AlarmList(ctx)
		if assert.NoError(c, err) {
			assert.NotEmpty(c, alarms.Alarms)
		}
	}, 10*time.Second, 10*time.Millisecond)

	_, err := sched.Client(t, ctx).ScheduleJob(ctx,
		sched.JobNowJob("test-job-after-quota", "default", q.wf.Dapr().AppID()))
	require.Error(t, err)
	require.Contains(t, err.Error(), "database space exceeded")

	cl := q.wf.BackendClient(t, ctx)
	_, err = cl.ScheduleNewWorkflow(ctx, "timerFlow")
	require.Error(t, err)
	require.Contains(t, err.Error(), "database space exceeded")

	sched.RecoverQuota(t, ctx, "quota-test/")

	id, err := cl.ScheduleNewWorkflow(ctx, "timerFlow")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
}
