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

package taskexecutionid

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(single))
}

type single struct {
	workflow *workflow.Workflow
}

func (e *single) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *single) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, e.workflow.Registry().AddOrchestratorN("single", func(ctx *task.OrchestrationContext) (any, error) {
		err := ctx.CallActivity("FailActivity", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: 10 * time.Millisecond,
		})).Await(nil)
		if err != nil {
			return nil, err
		}
		return nil, nil
	}))

	executionMap := make(map[string]int)
	var executionID string

	require.NoError(t, e.workflow.Registry().AddActivityN("FailActivity", func(ctx task.ActivityContext) (any, error) {
		executionMap[ctx.GetTaskExecutionID()] = executionMap[ctx.GetTaskExecutionID()] + 1
		executionID = ctx.GetTaskExecutionID()
		if executionMap[ctx.GetTaskExecutionID()] == 3 {
			return nil, nil
		}
		return nil, errors.New("activity failure")
	}))

	cl := e.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewOrchestration(ctx, "single")
	require.NoError(t, err)

	_, err = cl.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	_, err = uuid.Parse(executionID)
	require.NoError(t, err)
	require.Equal(t, 3, executionMap[executionID])
}
