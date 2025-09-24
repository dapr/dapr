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

package continueasnew

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activity))
}

type activity struct {
	workflow *workflow.Workflow
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	var cont atomic.Bool
	var barCalled atomic.Int64

	reg := a.workflow.Registry()
	require.NoError(t, reg.AddOrchestratorN("can", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		require.NoError(t, ctx.GetInput(&input))
		if cont.Load() {
			assert.Equal(t, "second call", input)
		} else {
			assert.Equal(t, "first call", input)
		}

		require.NoError(t, ctx.CallActivity("bar").Await(nil))

		if cont.CompareAndSwap(false, true) {
			ctx.ContinueAsNew("second call")
		}

		return nil, nil
	}))

	require.NoError(t, reg.AddOrchestratorN("can-keep", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		require.NoError(t, ctx.GetInput(&input))
		if cont.Load() {
			assert.Equal(t, "second call", input)
		} else {
			assert.Equal(t, "first call", input)
		}

		require.NoError(t, ctx.CallActivity("bar").Await(nil))

		if cont.CompareAndSwap(false, true) {
			ctx.ContinueAsNew("second call", task.WithKeepUnprocessedEvents())
		}

		return nil, nil
	}))

	require.NoError(t, reg.AddOrchestratorN("can-keep-many", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		require.NoError(t, ctx.GetInput(&input))
		if cont.Load() {
			assert.Equal(t, "second call", input)
		} else {
			assert.Equal(t, "first call", input)
		}

		for range 30 {
			ctx.CallActivity("bar")
		}

		if cont.CompareAndSwap(false, true) {
			ctx.ContinueAsNew("second call", task.WithKeepUnprocessedEvents())
		}

		return nil, nil
	}))

	require.NoError(t, reg.AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		barCalled.Add(1)
		return "", nil
	}))

	client := a.workflow.BackendClient(t, ctx)

	barCalled.Store(0)
	cont.Store(false)
	id, err := client.ScheduleNewOrchestration(ctx, "can",
		api.WithInstanceID("cani"),
		api.WithInput("first call"),
	)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), barCalled.Load())
	time.Sleep(time.Second)
	assert.Equal(t, int64(2), barCalled.Load())

	barCalled.Store(0)
	cont.Store(false)
	id, err = client.ScheduleNewOrchestration(ctx, "can-keep",
		api.WithInstanceID("cank"),
		api.WithInput("first call"),
	)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), barCalled.Load())
	time.Sleep(time.Second)
	assert.Equal(t, int64(2), barCalled.Load())

	barCalled.Store(0)
	cont.Store(false)
	id, err = client.ScheduleNewOrchestration(ctx, "can-keep-many",
		api.WithInstanceID("canm"),
		api.WithInput("first call"),
	)
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(30), barCalled.Load())
	}, time.Second*10, time.Millisecond*10)
	time.Sleep(time.Second)
	assert.Equal(t, int64(30), barCalled.Load())
}
