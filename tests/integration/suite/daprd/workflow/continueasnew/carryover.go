/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package continueasnew

import (
	"context"
	"encoding/json"
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
	suite.Register(new(carryover))
}

type carryover struct {
	workflow *workflow.Workflow
}

func (c *carryover) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *carryover) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)
	reg := c.workflow.Registry()
	client := c.workflow.BackendClient(t, ctx)

	t.Run("with keep preserves buffered events", func(t *testing.T) {
		var iteration atomic.Int32
		require.NoError(t, reg.AddOrchestratorN("can-keep-ev", func(ctx *task.OrchestrationContext) (any, error) {
			n := iteration.Add(1)
			if n == 1 {
				ctx.ContinueAsNew("second", task.WithKeepUnprocessedEvents())
				return nil, nil
			}
			var got string
			ctx.WaitForSingleEvent("ev", 2*time.Second).Await(&got)
			if got == "" {
				return "no-event", nil
			}
			return got, nil
		}))

		id, err := client.ScheduleNewOrchestration(ctx, "can-keep-ev",
			api.WithInstanceID("can-keep-ev-i"),
			api.WithInput("first"),
		)
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		require.NoError(t, client.RaiseEvent(ctx, id, "ev",
			api.WithEventPayload("hello")))

		meta, err := client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `"hello"`, meta.GetOutput().GetValue())
	})

	t.Run("keep preserves multiple event types", func(t *testing.T) {
		type state struct {
			Iteration int      `json:"iteration"`
			Collected []string `json:"collected"`
		}
		require.NoError(t, reg.AddOrchestratorN("can-multi-ev", func(ctx *task.OrchestrationContext) (any, error) {
			var st state
			require.NoError(t, ctx.GetInput(&st))

			if st.Iteration == 0 {
				st.Iteration++
				ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
				return nil, nil
			}

			for _, name := range []string{"alpha", "beta", "gamma"} {
				var val string
				ctx.WaitForSingleEvent(name, 2*time.Second).Await(&val)
				if val == "" {
					break
				}
				st.Collected = append(st.Collected, val)
			}
			return st, nil
		}))

		id, err := client.ScheduleNewOrchestration(ctx, "can-multi-ev",
			api.WithInstanceID("can-multi-ev-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		require.NoError(t, client.RaiseEvent(ctx, id, "alpha", api.WithEventPayload("a1")))
		require.NoError(t, client.RaiseEvent(ctx, id, "beta", api.WithEventPayload("b1")))
		require.NoError(t, client.RaiseEvent(ctx, id, "gamma", api.WithEventPayload("g1")))

		meta, err := client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		assert.Equal(t, []string{"a1", "b1", "g1"}, result.Collected)
	})

	t.Run("keep with activity in same iteration", func(t *testing.T) {
		var activityCalls atomic.Int32
		require.NoError(t, reg.AddActivityN("can-act", func(c task.ActivityContext) (any, error) {
			activityCalls.Add(1)
			return nil, nil
		}))

		require.NoError(t, reg.AddOrchestratorN("can-keep-act", func(ctx *task.OrchestrationContext) (any, error) {
			var inc int
			require.NoError(t, ctx.GetInput(&inc))

			require.NoError(t, ctx.CallActivity("can-act").Await(nil))

			var val string
			ctx.WaitForSingleEvent("next", 2*time.Second).Await(&val)
			if val == "" {
				return inc, nil
			}

			ctx.ContinueAsNew(inc+1, task.WithKeepUnprocessedEvents())
			return nil, nil
		}))

		id, err := client.ScheduleNewOrchestration(ctx, "can-keep-act",
			api.WithInstanceID("can-keep-act-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		for range 3 {
			require.NoError(t, client.RaiseEvent(ctx, id, "next", api.WithEventPayload("go")))
			time.Sleep(100 * time.Millisecond)
		}

		meta, err := client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `3`, meta.GetOutput().GetValue())
		assert.GreaterOrEqual(t, activityCalls.Load(), int32(3))
	})

	t.Run("multiple CAN cycles preserve events across all", func(t *testing.T) {
		type state struct {
			Cycle    int      `json:"cycle"`
			Received []string `json:"received"`
		}
		require.NoError(t, reg.AddOrchestratorN("can-multi-cycle", func(ctx *task.OrchestrationContext) (any, error) {
			var st state
			require.NoError(t, ctx.GetInput(&st))

			var val string
			ctx.WaitForSingleEvent("data", 3*time.Second).Await(&val)
			if val == "" {
				return st, nil
			}

			st.Received = append(st.Received, val)
			st.Cycle++
			ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
			return nil, nil
		}))

		id, err := client.ScheduleNewOrchestration(ctx, "can-multi-cycle",
			api.WithInstanceID("can-multi-cycle-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationStart(ctx, id)
		require.NoError(t, err)

		for i := range 5 {
			require.NoError(t, client.RaiseEvent(ctx, id, "data",
				api.WithEventPayload("v"+string(rune('0'+i)))))
		}

		meta, err := client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		assert.Equal(t, 5, result.Cycle)
		assert.Len(t, result.Received, 5)
	})
}
