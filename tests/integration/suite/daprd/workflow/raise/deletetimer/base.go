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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(base))
}

type base struct {
	workflow *workflow.Workflow
}

func (d *base) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *base) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.WaitForSingleEvent("bar", time.Minute).Await(nil))
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

	require.NoError(t, cl.RaiseEvent(ctx, id, "bar"))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*20, 10*time.Millisecond)

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	var found bool
	for _, e := range hist.GetEvents() {
		tc := e.GetTimerCreated()
		if tc == nil {
			continue
		}
		found = true
		ee := tc.GetExternalEvent()
		require.NotNil(t, ee, "expected TimerCreated to have origin.external_event set")
		assert.Equal(t, "bar", ee.GetName())
		_, ok := tc.GetOrigin().(*protos.TimerCreatedEvent_ExternalEvent)
		require.True(t, ok, "expected origin to be ExternalEvent, got %T", tc.GetOrigin())
	}
	require.True(t, found, "expected at least one TimerCreated event in history")
}
