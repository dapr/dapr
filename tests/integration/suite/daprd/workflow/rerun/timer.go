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

package rerun

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(timer))
}

type timer struct {
	workflow *workflow.Workflow
}

func (i *timer) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *timer) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddOrchestratorN("not-activity", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Second).Await(nil))
		return nil, nil
	})
	i.workflow.Registry().AddOrchestratorN("active-timer", func(ctx *task.OrchestrationContext) (any, error) {
		as1 := ctx.CreateTimer(time.Second * 5)
		as2 := ctx.CallActivity("bar")
		require.NoError(t, as2.Await(nil))
		require.NoError(t, as1.Await(nil))
		return nil, nil
	})
	i.workflow.Registry().AddOrchestratorN("completed-timer", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Second).Await(nil))
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	i.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		time.Sleep(time.Second * 2)
		return nil, nil
	})

	client := i.workflow.BackendClient(t, ctx)

	t.Run("not-activity", func(t *testing.T) {
		id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)

		_, err = client.RerunWorkflowFromEvent(ctx, id, 0)
		assert.Equal(t, status.Error(codes.NotFound, "'abc' target event ID '0' is not a TaskScheduled event"), err)
	})

	t.Run("active-timer", func(t *testing.T) {
		id, err := client.ScheduleNewOrchestration(ctx, "active-timer", api.WithInstanceID("xyz"))
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		_, err = client.RerunWorkflowFromEvent(ctx, id, 0)
		assert.Equal(t, status.Error(codes.NotFound, "'xyz' target event ID '0' is not a TaskScheduled event"), err)
	})

	t.Run("completed-timer", func(t *testing.T) {
		id, err := client.ScheduleNewOrchestration(ctx, "completed-timer", api.WithInstanceID("ijk"))
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		newID, err := client.RerunWorkflowFromEvent(ctx, id, 1)
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, newID)
		require.NoError(t, err)
	})
}
