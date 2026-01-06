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
	suite.Register(new(fromchild))
}

type fromchild struct {
	workflow *workflow.Workflow
}

func (f *fromchild) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fromchild) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallSubOrchestrator("aaa", task.WithSubOrchestrationInstanceID("def")).Await(nil))
		return nil, nil
	})
	f.workflow.Registry().AddOrchestratorN("aaa", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	client := f.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, "abc")
	require.NoError(t, err)

	_, err = client.RerunWorkflowFromEvent(ctx, api.InstanceID("def"), 0)
	assert.Equal(t, status.Error(codes.InvalidArgument, "'def': cannot rerun from child-workflows"), err)
}
