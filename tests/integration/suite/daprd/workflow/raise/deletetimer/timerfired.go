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

package deletetimer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(timerfired))
}

type timerfired struct {
	workflow *workflow.Workflow
}

func (d *timerfired) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *timerfired) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		//nolint:nilerr
		if err := ctx.WaitForSingleEvent("bar", time.Second*5).Await(nil); err != nil {
			// ErrTaskCanceled is expected when the timer fires.
			return "timed_out", nil
		}
		return "event_received", nil
	})

	cl := d.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs")
		if assert.NotEmpty(c, keys) {
			assert.Contains(c, keys[0], "timer-0")
		}
	}, time.Second*20, 10*time.Millisecond)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*20, 10*time.Millisecond)
}
