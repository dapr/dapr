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
	"errors"
	"fmt"
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
	suite.Register(new(edges))
}

type edges struct {
	workflow *workflow.Workflow
}

func (e *edges) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *edges) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)
	reg := e.workflow.Registry()
	client := e.workflow.BackendClient(t, ctx)

	t.Run("partial consumption preserves unconsumed events", func(t *testing.T) {
		type state struct {
			Phase int    `json:"phase"`
			GotA  string `json:"gotA"`
			GotB  string `json:"gotB"`
		}
		reg.AddWorkflowN("edge-partial", func(ctx *task.WorkflowContext) (any, error) {
			var st state
			require.NoError(t, ctx.GetInput(&st))

			switch st.Phase {
			case 0:
				require.NoError(t, ctx.WaitForSingleEvent("evA", time.Minute).Await(&st.GotA))
				st.Phase = 1
				ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
				return nil, nil
			case 1:
				require.NoError(t, ctx.WaitForSingleEvent("evB", 3*time.Second).Await(&st.GotB))
				return st, nil
			}
			return nil, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-partial",
			api.WithInstanceID("edge-partial-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, id)
		require.NoError(t, err)

		require.NoError(t, client.RaiseEvent(ctx, id, "evA", api.WithEventPayload("a-val")))
		require.NoError(t, client.RaiseEvent(ctx, id, "evB", api.WithEventPayload("b-val")))

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		assert.Equal(t, "a-val", result.GotA)
		assert.Equal(t, "b-val", result.GotB)
	})

	t.Run("FIFO ordering preserved across CAN boundaries", func(t *testing.T) {
		type state struct {
			Received []string `json:"received"`
		}
		reg.AddWorkflowN("edge-fifo", func(ctx *task.WorkflowContext) (any, error) {
			var st state
			require.NoError(t, ctx.GetInput(&st))

			var val string
			ctx.WaitForSingleEvent("item", 3*time.Second).Await(&val)
			if val == "" {
				return st, nil
			}

			st.Received = append(st.Received, val)
			ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
			return nil, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-fifo",
			api.WithInstanceID("edge-fifo-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, id)
		require.NoError(t, err)

		for i := range 7 {
			require.NoError(t, client.RaiseEvent(ctx, id, "item",
				api.WithEventPayload(fmt.Sprintf("msg-%d", i))))
		}

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		expected := []string{"msg-0", "msg-1", "msg-2", "msg-3", "msg-4", "msg-5", "msg-6"}
		assert.Equal(t, expected, result.Received)
	})

	t.Run("immediate CAN tight-loop without events", func(t *testing.T) {
		reg.AddWorkflowN("edge-tightloop", func(ctx *task.WorkflowContext) (any, error) {
			var counter int
			require.NoError(t, ctx.GetInput(&counter))

			if counter >= 30 {
				return counter, nil
			}

			ctx.ContinueAsNew(counter + 1)
			return nil, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-tightloop",
			api.WithInstanceID("edge-tightloop-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `30`, meta.GetOutput().GetValue())
	})

	t.Run("CAN after activity failure", func(t *testing.T) {
		require.NoError(t, reg.AddActivityN("edge-fail-act", func(c task.ActivityContext) (any, error) {
			return nil, errors.New("intentional failure")
		}))

		reg.AddWorkflowN("edge-act-fail", func(ctx *task.WorkflowContext) (any, error) {
			var attempt int
			require.NoError(t, ctx.GetInput(&attempt))

			if attempt >= 3 {
				return "gave-up", nil
			}

			if err := ctx.CallActivity("edge-fail-act").Await(nil); err != nil {
				ctx.ContinueAsNew(attempt + 1)
				return nil, nil //nolint:nilerr
			}
			return "unexpected-success", nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-act-fail",
			api.WithInstanceID("edge-act-fail-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `"gave-up"`, meta.GetOutput().GetValue())
	})

	t.Run("custom status resets after CAN", func(t *testing.T) {
		var secondRun atomic.Bool
		reg.AddWorkflowN("edge-status", func(ctx *task.WorkflowContext) (any, error) {
			if secondRun.CompareAndSwap(false, true) {
				ctx.SetCustomStatus("phase-1")
				ctx.ContinueAsNew("round2")
				return nil, nil
			}
			return "done", nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-status",
			api.WithInstanceID("edge-status-i"),
			api.WithInput("round1"),
		)
		require.NoError(t, err)

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
		assert.Empty(t, meta.GetCustomStatus().GetValue())
	})

	t.Run("keep events with timer in same iteration", func(t *testing.T) {
		type state struct {
			TimerFired bool   `json:"timerFired"`
			EventVal   string `json:"eventVal"`
		}
		reg.AddWorkflowN("edge-timer-keep", func(ctx *task.WorkflowContext) (any, error) {
			var st state
			require.NoError(t, ctx.GetInput(&st))

			if !st.TimerFired {
				require.NoError(t, ctx.CreateTimer(100*time.Millisecond).Await(nil))
				st.TimerFired = true
				ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
				return nil, nil
			}

			ctx.WaitForSingleEvent("data", 3*time.Second).Await(&st.EventVal)
			return st, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "edge-timer-keep",
			api.WithInstanceID("edge-timer-keep-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, id)
		require.NoError(t, err)

		require.NoError(t, client.RaiseEvent(ctx, id, "data",
			api.WithEventPayload("timer-test")))

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		assert.True(t, result.TimerFired)
		assert.Equal(t, "timer-test", result.EventVal)
	})
}
