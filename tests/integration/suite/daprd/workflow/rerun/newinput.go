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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(newinput))
}

type newinput struct {
	workflow *workflow.Workflow
}

func (n *newinput) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *newinput) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	var input atomic.Pointer[string]
	n.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("bar", task.WithActivityInput("helloworld")).Await(nil))
		return nil, nil
	})
	n.workflow.Registry().AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		var got string
		require.NoError(t, ctx.GetInput(&got))
		input.Store(&got)
		return nil, nil
	})
	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "foo", api.WithInstanceID("abc"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "helloworld", *input.Load())

	newID, err := client.RerunWorkflowFromEvent(ctx, api.InstanceID("abc"), 0, api.WithRerunInput("newinput"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
	assert.Equal(t, "newinput", *input.Load())
}
