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

package list

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(duration))
}

// duration lists instances by duration: the completed time minus the created
// time, or for an instance that did not complete, the time of the call minus
// the created time. The minimum duration is inclusive and the maximum
// duration is exclusive.
type duration struct {
	workflow *workflow.Workflow
}

func (d *duration) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *duration) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const longRun = 600 * time.Millisecond
	d.workflow.Registry().AddWorkflowN("short", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	d.workflow.Registry().AddWorkflowN("long", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(longRun).Await(nil)
	})
	d.workflow.Registry().AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	client := d.workflow.BackendClient(t, ctx)

	// The running instance starts first, so it runs for longer than the
	// long instance.
	const short, long, running = "short-1", "long-1", "running-1"
	_, err := client.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID(running))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, running)
	require.NoError(t, err)
	for _, wf := range []struct{ id, name string }{{short, "short"}, {long, "long"}} {
		_, err = client.ScheduleNewWorkflow(ctx, wf.name, api.WithInstanceID(api.InstanceID(wf.id)))
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(wf.id))
		require.NoError(t, err)
	}

	byID := make(map[string]*protos.WorkflowInstanceSummary)
	for _, sum := range fworkflow.ListAllInstances(t, ctx, client) {
		byID[sum.GetInstanceId()] = sum
	}
	require.Len(t, byID, 3)
	ran := func(id string) time.Duration {
		return byID[id].GetCompletedTimestamp().AsTime().Sub(byID[id].GetCreatedTimestamp().AsTime())
	}
	shortRan, longRan := ran(short), ran(long)
	require.GreaterOrEqual(t, longRan, longRun)
	require.Less(t, shortRan, longRun)
	require.Nil(t, byID[running].GetCompletedTimestamp())

	// Between the short and the long run.
	cut := (shortRan + longRun) / 2
	assert.ElementsMatch(t, []string{long, running},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesMinDuration(cut)))
	assert.ElementsMatch(t, []string{short},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesMaxDuration(cut)))

	// The bounds at the exact duration of the long instance.
	assert.ElementsMatch(t, []string{long, running},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesMinDuration(longRan)))
	assert.ElementsMatch(t, []string{short},
		fworkflow.ListedIDs(t, ctx, client, api.WithListInstancesMaxDuration(longRan)))
	assert.ElementsMatch(t, []string{long}, fworkflow.ListedIDs(t, ctx, client,
		api.WithListInstancesMinDuration(longRan),
		api.WithListInstancesMaxDuration(longRan+time.Nanosecond),
	))

	// The running instance runs until the time of the call, which is more
	// than the long run and less than an hour.
	assert.ElementsMatch(t, []string{running}, fworkflow.ListedIDs(t, ctx, client,
		api.WithListInstancesMinDuration(longRan+time.Nanosecond),
		api.WithListInstancesMaxDuration(time.Hour),
	))
}
