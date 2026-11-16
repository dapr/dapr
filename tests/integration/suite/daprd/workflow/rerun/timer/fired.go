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

package timer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fired))
}

// fired reruns from an activity scheduled after a timer had fired. The rerun
// keeps that timer finished: its creation stays ahead of its firing in the
// new history, and it is not created and fired again.
type fired struct {
	workflow *workflow.Workflow
}

func (f *fired) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fired) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddWorkflowN("fired-timer", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Millisecond*100).Await(nil))
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	f.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := f.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "fired-timer", api.WithInstanceID("fired"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Event 0 is the timer, event 1 the activity scheduled once it fired.
	newID, err := client.RerunWorkflowFromEvent(ctx, id, 1)
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	hist, err := client.GetInstanceHistory(ctx, newID)
	require.NoError(t, err)
	created := -1
	var firings []int
	for i, e := range hist.GetEvents() {
		if e.GetTimerCreated() != nil && e.GetEventId() == 0 {
			created = i
		}
		if e.GetTimerFired() != nil && e.GetTimerFired().GetTimerId() == 0 {
			firings = append(firings, i)
		}
	}
	require.GreaterOrEqual(t, created, 0, "the timer is in the rerun's history")
	require.Len(t, firings, 1, "the timer fires once")
	assert.Less(t, created, firings[0], "the timer is created before it fires")
}
