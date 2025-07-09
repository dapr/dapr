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
	"sync"
	"sync/atomic"
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
	suite.Register(new(parallel))
}

type parallel struct {
	workflow *workflow.Workflow
}

func (e *parallel) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *parallel) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, e.workflow.Registry().AddOrchestratorN("parallel", func(ctx *task.OrchestrationContext) (any, error) {
		t1 := ctx.CallActivity("FailActivity", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: 10 * time.Millisecond,
		}))

		t2 := ctx.CallActivity("FailActivity", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: 10 * time.Millisecond,
		}))

		t3 := ctx.CallActivity("FailActivity", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: 10 * time.Millisecond,
		}))

		err := t1.Await(nil)
		if err != nil {
			return nil, err
		}

		err = t2.Await(nil)
		if err != nil {
			return nil, err
		}

		err = t3.Await(nil)
		if err != nil {
			return nil, err
		}

		return nil, nil
	}))

	executionMap := sync.Map{}

	require.NoError(t, e.workflow.Registry().AddActivityN("FailActivity", func(ctx task.ActivityContext) (any, error) {
		count, _ := executionMap.LoadOrStore(ctx.GetTaskExecutionID(), &atomic.Int32{})
		value := count.(*atomic.Int32)
		if value.Load() == 2 {
			return nil, nil
		}
		value.Add(1)
		return nil, errors.New("activity failure")
	}))

	cl := e.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewOrchestration(ctx, "parallel")
	require.NoError(t, err)

	_, err = cl.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	executionMap.Range(func(k, v interface{}) bool {
		_, err = uuid.Parse(k.(string))
		require.NoError(t, err)
		require.EqualValues(t, 2, v.(*atomic.Int32).Load())
		return true
	})
}
