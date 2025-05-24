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

package raise

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
	suite.Register(new(input))
}

type input struct {
	workflow *workflow.Workflow
}

func (i *input) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *input) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddOrchestratorN("simple-event", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.WaitForSingleEvent("abc1", time.Hour).Await(nil))
		return nil, nil
	})

	client := i.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "simple-event")
	require.NoError(t, err)
	time.Sleep(time.Second * 2)
	require.NoError(t, client.RaiseEvent(ctx, id, "abc1"))
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	_, err = client.RerunWorkflowFromEvent(ctx, id, 0, api.WithRerunInput("hello"))
	assert.Equal(t, status.Error(codes.InvalidArgument, "cannot write input to timer event '0'"), err)
}
