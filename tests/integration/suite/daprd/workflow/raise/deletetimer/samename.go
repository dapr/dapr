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
	suite.Register(new(samename))
}

// samename covers sequential waits on the SAME event name. Timers cancelled
// in earlier turns look like pending ones in history (TimerCreated, no
// TimerFired), so they must not absorb later cancellations and leak the live
// timer's reminder. Asserted mid-flight: the completion-path cleanup would
// mask the leak if only checked at the end.
type samename struct {
	workflow *workflow.Workflow
}

func (d *samename) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *samename) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		for range 3 {
			if err := ctx.WaitForSingleEvent("bar", time.Minute).Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	cl := d.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs")
		if assert.Len(c, keys, 1) {
			assert.Contains(c, keys[0], "timer-0")
		}
	}, time.Second*20, 10*time.Millisecond)

	// First event: cancels timer-0, arms the second wait (timer-1).
	require.NoError(t, cl.RaiseEvent(ctx, id, "bar"))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs")
		if assert.Len(c, keys, 1) {
			assert.Contains(c, keys[0], "timer-1")
		}
	}, time.Second*20, 10*time.Millisecond)

	// Second event: must cancel timer-1, not re-target the long-gone timer-0
	// and leak timer-1.
	require.NoError(t, cl.RaiseEvent(ctx, id, "bar"))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs")
		if assert.Len(c, keys, 1) {
			assert.Contains(c, keys[0], "timer-2")
		}
	}, time.Second*20, 10*time.Millisecond)

	require.NoError(t, cl.RaiseEvent(ctx, id, "bar"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*20, 10*time.Millisecond)
}
