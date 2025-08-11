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
	suite.Register(new(instanceid))
}

type instanceid struct {
	workflow *workflow.Workflow
}

func (i *instanceid) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *instanceid) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	i.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})
	client := i.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, id, newID)

	newID, err = client.RerunWorkflowFromEvent(ctx, id, 0, api.WithRerunNewInstanceID("helloworld"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.NotEqual(t, "helloworld", newID)

	_, err = client.RerunWorkflowFromEvent(ctx, id, 0, api.WithRerunNewInstanceID(id))
	assert.Equal(t, status.Error(codes.InvalidArgument, "rerun workflow instance ID must be different from the original instance ID"), err)
}
