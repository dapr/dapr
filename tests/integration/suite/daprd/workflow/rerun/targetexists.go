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
	suite.Register(new(targetexists))
}

type targetexists struct {
	workflow *workflow.Workflow
}

func (e *targetexists) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *targetexists) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry(0).AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar").Await(nil))
		return nil, nil
	})
	e.workflow.Registry(0).AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := e.workflow.BackendClient(t, ctx, 0)

	_, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, api.InstanceID("abc"))
	require.NoError(t, err)

	_, err = client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("xyz"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, api.InstanceID("xyz"))
	require.NoError(t, err)

	_, err = client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 0, api.WithRerunNewInstanceID("xyz"))
	require.Error(t, err)

	assert.Equal(t, status.Error(codes.AlreadyExists, "workflow 'xyz' has already been created"), err)
}
