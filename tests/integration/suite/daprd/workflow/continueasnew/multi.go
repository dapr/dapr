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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multi))
}

type multi struct {
	workflow *workflow.Workflow
}

func (m *multi) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multi) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)
	reg := m.workflow.Registry()

	client := m.workflow.BackendClient(t, ctx)

	t.Run("timer", func(t *testing.T) {
		var timerCount atomic.Int32
		reg.AddWorkflowN("can-timer", func(ctx *task.WorkflowContext) (any, error) {
			var inc int
			require.NoError(t, ctx.GetInput(&inc))
			timerCount.Add(1)

			require.NoError(t, ctx.CreateTimer(time.Millisecond*100).Await(nil))

			if inc < 3 {
				ctx.ContinueAsNew(inc + 1)
			}
			return inc, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "can-timer",
			api.WithInstanceID("can-timer-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `3`, meta.GetOutput().GetValue())
		assert.GreaterOrEqual(t, timerCount.Load(), int32(4))
	})

	t.Run("noinput", func(t *testing.T) {
		var noinputCount atomic.Int32
		reg.AddWorkflowN("can-noinput", func(ctx *task.WorkflowContext) (any, error) {
			n := noinputCount.Add(1)
			if n < 4 {
				ctx.ContinueAsNew("same")
			}
			return n, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "can-noinput",
			api.WithInstanceID("can-noinput-i"),
			api.WithInput("same"),
		)
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `4`, meta.GetOutput().GetValue())
		assert.Equal(t, int32(4), noinputCount.Load())
	})

	t.Run("events_between_iterations", func(t *testing.T) {
		type state struct {
			Count  int      `json:"count"`
			Values []string `json:"values"`
		}

		reg.AddWorkflowN("can-events-between", func(ctx *task.WorkflowContext) (any, error) {
			var s state
			require.NoError(t, ctx.GetInput(&s))

			var val string
			require.NoError(t, ctx.WaitForSingleEvent("data", time.Minute).Await(&val))
			s.Values = append(s.Values, val)
			s.Count++

			if s.Count < 3 {
				ctx.ContinueAsNew(s)
			}
			return s, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "can-events-between",
			api.WithInstanceID("can-events-between-i"),
			api.WithInput(state{}),
		)
		require.NoError(t, err)
		_, err = client.WaitForWorkflowStart(ctx, id)
		require.NoError(t, err)

		for i := range 3 {
			require.NoError(t, client.RaiseEvent(ctx, id, "data",
				api.WithEventPayload(fmt.Sprintf("v%d", i))))
			time.Sleep(200 * time.Millisecond)
		}

		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		var result state
		require.NoError(t, json.Unmarshal([]byte(meta.GetOutput().GetValue()), &result))
		assert.Equal(t, 3, result.Count)
		assert.Equal(t, []string{"v0", "v1", "v2"}, result.Values)
	})

	t.Run("child", func(t *testing.T) {
		var childCalls atomic.Int32
		reg.AddWorkflowN("can-child-parent", func(ctx *task.WorkflowContext) (any, error) {
			var inc int
			require.NoError(t, ctx.GetInput(&inc))

			var childResult int
			require.NoError(t, ctx.CallChildWorkflow("can-child-child",
				task.WithChildWorkflowInput(inc)).Await(&childResult))

			if inc < 2 {
				ctx.ContinueAsNew(inc + 1)
			}
			return childResult, nil
		})

		reg.AddWorkflowN("can-child-child", func(ctx *task.WorkflowContext) (any, error) {
			var input int
			require.NoError(t, ctx.GetInput(&input))
			childCalls.Add(1)
			return input * 10, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "can-child-parent",
			api.WithInstanceID("can-child-parent-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, `20`, meta.GetOutput().GetValue())
		assert.Equal(t, int32(3), childCalls.Load())
	})

	t.Run("generation", func(t *testing.T) {
		reg.AddWorkflowN("can-generation", func(ctx *task.WorkflowContext) (any, error) {
			var inc int
			require.NoError(t, ctx.GetInput(&inc))
			if inc < 5 {
				ctx.ContinueAsNew(inc + 1)
			}
			return inc, nil
		})

		id, err := client.ScheduleNewWorkflow(ctx, "can-generation",
			api.WithInstanceID("can-generation-i"),
			api.WithInput(0),
		)
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		db := m.workflow.DB().GetConnection(t)
		tableName := m.workflow.DB().TableName()
		appID := m.workflow.Dapr().AppID()
		actorType := "dapr.internal.default." + appID + ".workflow"
		metaKey := appID + "||" + actorType + "||can-generation-i||metadata"

		var val string
		var isBin bool
		require.NoError(t, db.QueryRowContext(ctx,
			fmt.Sprintf("SELECT value, is_binary FROM '%s' WHERE key = ?", tableName),
			metaKey,
		).Scan(&val, &isBin))
		require.True(t, isBin)

		raw, err := base64.StdEncoding.DecodeString(val)
		require.NoError(t, err)
		var wfMeta backend.BackendWorkflowStateMetadata
		require.NoError(t, proto.Unmarshal(raw, &wfMeta))
		assert.Equal(t, uint64(2), wfMeta.GetGeneration(),
			"generation should be 2: initial(1) + one CAN execution(+1)")
	})
}
