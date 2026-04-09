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
	"fmt"
	"sync"
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
	suite.Register(new(semaphore))
}

// semaphore models the core pattern from the dapr-semaphore-workflow project:
// a coordinator workflow receives request events, dispatches activities for
// each (up to a concurrency limit), and CAN's after each event. When events
// arrive faster than the concurrency limit allows dispatching, the workflow
// enters a tight CAN loop (consume event -> CAN without activity dispatch),
// which triggers MaxContinueAsNewCount.
type semaphore struct {
	workflow *workflow.Workflow
}

func (s *semaphore) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *semaphore) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	reg := s.workflow.Registry()

	const totalRequests = 30

	var mu sync.Mutex
	dispatched := make(map[string]int)

	type coordState struct {
		Pending    []string `json:"pending"`
		Dispatched int      `json:"dispatched"`
	}

	require.NoError(t, reg.AddActivityN("sem-dispatch", func(c task.ActivityContext) (any, error) {
		var reqID string
		require.NoError(t, c.GetInput(&reqID))
		mu.Lock()
		dispatched[reqID]++
		mu.Unlock()
		return reqID, nil
	}))

	reg.AddWorkflowN("sem-coordinator", func(ctx *task.WorkflowContext) (any, error) {
		var st coordState
		require.NoError(t, ctx.GetInput(&st))

		for len(st.Pending) > 0 && st.Dispatched < totalRequests {
			reqID := st.Pending[0]
			st.Pending = st.Pending[1:]
			st.Dispatched++
			require.NoError(t, ctx.CallActivity("sem-dispatch",
				task.WithActivityInput(reqID),
			).Await(nil))
		}

		if st.Dispatched >= totalRequests {
			return st.Dispatched, nil
		}

		// Wait for the next request event. WithKeepUnprocessedEvents
		// carries over any buffered inbox events that arrived while this
		// iteration was running, so st.Pending only holds the single
		// event consumed here — the remaining inbox events become
		// NewEvents on the next CAN iteration.
		var reqID string
		ctx.WaitForSingleEvent("request", 5*time.Second).Await(&reqID)
		if reqID != "" {
			st.Pending = append(st.Pending, reqID)
		}

		ctx.ContinueAsNew(st, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := s.workflow.BackendClient(t, ctx)

	coordID, err := client.ScheduleNewWorkflow(ctx, "sem-coordinator",
		api.WithInstanceID("sem-coord"),
		api.WithInput(coordState{}),
	)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, coordID)
	require.NoError(t, err)

	for i := range totalRequests {
		reqID := fmt.Sprintf("req-%03d", i)
		require.NoError(t, client.RaiseEvent(ctx, coordID, "request",
			api.WithEventPayload(reqID)))
	}

	meta, err := client.WaitForWorkflowCompletion(ctx, coordID)
	require.NoError(t, err)
	require.NotNil(t, meta.GetOutput(), "coordinator should complete")

	mu.Lock()
	defer mu.Unlock()
	for reqID, count := range dispatched {
		assert.Equal(t, 1, count,
			"request %q dispatched %d times, expected 1", reqID, count)
	}
	assert.Len(t, dispatched, totalRequests,
		"all %d requests should have been dispatched", totalRequests)
}
