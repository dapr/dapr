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

package get

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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(times))
}

// times asserts the times that GetInstance returns, from the daprd of the
// app and from the daprd of another app. A completed instance has the time
// of its ExecutionCompleted event as its completion time. A running instance
// has no completion time. A PENDING instance has the time of its
// ExecutionStarted event as its creation time.
type times struct {
	workflow *workflow.Workflow
}

func (g *times) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t, workflow.WithDaprds(2))

	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *times) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	g.workflow.RegistryN(1).AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	g.workflow.RegistryN(1).AddWorkflowN("wait", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Minute).Await(nil)
	})

	other := g.workflow.BackendClient(t, ctx)
	client := g.workflow.BackendClientN(t, ctx, 1)
	appID := g.workflow.DaprN(1).AppID()

	_, err := client.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID("done"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, "done")
	require.NoError(t, err)

	_, err = client.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID("running"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, "running")
	require.NoError(t, err)

	// The start time is after the end of the test, so the instance stays
	// PENDING.
	before := time.Now()
	_, err = client.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID("pending"), api.WithStartTime(before.Add(time.Hour)))
	require.NoError(t, err)
	after := time.Now()

	hist, err := client.GetInstanceHistory(ctx, "done")
	require.NoError(t, err)
	var completedAt time.Time
	for _, e := range hist.GetEvents() {
		if e.GetExecutionCompleted() != nil {
			completedAt = e.GetTimestamp().AsTime()
		}
	}
	require.False(t, completedAt.IsZero())

	fetches := map[string]func(api.InstanceID) (*protos.WorkflowMetadata, error){
		"this app": func(id api.InstanceID) (*protos.WorkflowMetadata, error) {
			return client.FetchWorkflowMetadata(ctx, id)
		},
		"another app": func(id api.InstanceID) (*protos.WorkflowMetadata, error) {
			return other.FetchWorkflowMetadata(ctx, id, api.WithFetchAppID(appID))
		},
	}
	for name, fetch := range fetches {
		t.Run(name, func(t *testing.T) {
			meta, err := fetch("done")
			require.NoError(t, err)
			assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
			if assert.NotNil(t, meta.GetCompletedAt()) {
				assert.Equal(t, completedAt, meta.GetCompletedAt().AsTime())
			}

			meta, err = fetch("running")
			require.NoError(t, err)
			assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
			assert.Nil(t, meta.GetCompletedAt())

			meta, err = fetch("pending")
			require.NoError(t, err)
			assert.Equal(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus())
			assert.Nil(t, meta.GetCompletedAt())
			created := meta.GetCreatedAt().AsTime()
			assert.False(t, created.Before(before.Add(-time.Second)), "createdAt %v is before %v", created, before)
			assert.False(t, created.After(after.Add(time.Second)), "createdAt %v is after %v", created, after)
		})
	}
}
