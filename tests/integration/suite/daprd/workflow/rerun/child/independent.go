/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package child

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(independent))
}

type independent struct {
	workflow *workflow.Workflow
}

func (r *independent) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *independent) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var calls atomic.Int32
	var fail atomic.Bool
	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("bar").Await(nil)
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		var result string
		err := ctx.CallActivity("work").Await(&result)
		return result, err
	})
	reg.AddActivityN("work", func(ctx dworkflow.ActivityContext) (any, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, assert.AnError
		}
		return "done", nil
	})

	client := r.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	_, err = client.RerunWorkflowFromEvent(ctx, "abc", 0, dworkflow.WithRerunNewInstanceID("hello"))
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, "hello")
	require.NoError(t, err)

	ids, err := client.ListInstanceIDs(ctx)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"abc", "abc:0000", "hello", "hello:0000"}, ids.InstanceIds)

	// Each child is rerun independently, including a child created by a root
	// rerun. Neither original parent's history should receive a second result.
	for _, parent := range []string{"abc", "hello"} {
		before, historyErr := client.GetInstanceHistory(ctx, parent)
		require.NoError(t, historyErr)
		newID := "rerun-" + parent
		_, err = client.RerunWorkflowFromEvent(ctx, parent+":0000", 0, dworkflow.WithRerunNewInstanceID(newID))
		require.NoError(t, err)
		meta, completionErr := client.WaitForWorkflowCompletion(ctx, newID)
		require.NoError(t, completionErr)
		assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
		assert.Empty(t, meta.ParentInstanceId)
		assert.Equal(t, `"done"`, meta.Output.GetValue())
		history, historyErr := client.GetInstanceHistory(ctx, newID)
		require.NoError(t, historyErr)
		for _, event := range history.Events {
			if started := event.GetExecutionStarted(); started != nil {
				assert.Nil(t, started.GetParentInstance())
			}
		}
		after, historyErr := client.GetInstanceHistory(ctx, parent)
		require.NoError(t, historyErr)
		require.Len(t, after.Events, len(before.Events))
		for i := range before.Events {
			assert.True(t, proto.Equal(before.Events[i], after.Events[i]))
		}
	}
	assert.Equal(t, int32(4), calls.Load())

	// Recover an activity failure inside a child without replaying its parent.
	fail.Store(true)
	_, err = client.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("failed"))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, "failed")
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusFailed, meta.RuntimeStatus)
	before, err := client.GetInstanceHistory(ctx, "failed")
	require.NoError(t, err)
	fail.Store(false)
	_, err = client.RerunWorkflowFromEvent(ctx, "failed:0000", 0, dworkflow.WithRerunNewInstanceID("recovered"))
	require.NoError(t, err)
	meta, err = client.WaitForWorkflowCompletion(ctx, "recovered")
	require.NoError(t, err)
	assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	assert.Empty(t, meta.ParentInstanceId)
	assert.Equal(t, `"done"`, meta.Output.GetValue())
	after, err := client.GetInstanceHistory(ctx, "failed")
	require.NoError(t, err)
	require.Len(t, after.Events, len(before.Events))
	for i := range before.Events {
		assert.True(t, proto.Equal(before.Events[i], after.Events[i]))
	}
	assert.Equal(t, int32(6), calls.Load())
}
