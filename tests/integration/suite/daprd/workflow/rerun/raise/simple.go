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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(simple))
}

type simple struct {
	workflow *workflow.Workflow
}

func (s *simple) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *simple) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddOrchestratorN("simple-event", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.WaitForSingleEvent("abc1", time.Hour).Await(nil))
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "simple-event", api.WithInstanceID("abc"))
	require.NoError(t, err)
	time.Sleep(time.Second * 2)
	require.NoError(t, client.RaiseEvent(ctx, id, "abc1"))
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0)
	time.Sleep(time.Second * 2)
	require.NoError(t, client.RaiseEvent(ctx, newID, "abc1"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, newID)
	require.NoError(t, err)
}
