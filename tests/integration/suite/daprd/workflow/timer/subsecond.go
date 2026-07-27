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

package timer

import (
	"context"
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
	suite.Register(new(subsecond))
}

// subsecond guards the regression in which short workflow timers fired
// ahead of their requested duration because reminder timestamps were
// serialized with whole-second precision and registration time was
// truncated to the prior whole second.
type subsecond struct {
	workflow *workflow.Workflow
}

func (s *subsecond) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *subsecond) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const requested = 2 * time.Second

	s.workflow.Registry().AddWorkflowN("subsecond-timer", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CreateTimer(requested).Await(nil)
	})

	client := s.workflow.BackendClient(t, ctx)

	start := time.Now()
	id, err := client.ScheduleNewWorkflow(ctx, "subsecond-timer", api.WithInstanceID("subsecond-timer"))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	elapsed := time.Since(start)

	// The timer must never fire before the requested duration. Prior to
	// sub-second precision support, this assertion failed by up to ~1s
	// because RegisteredTime was truncated to the previous whole second.
	assert.GreaterOrEqual(t, elapsed, requested,
		"workflow timer fired early: elapsed=%s, requested=%s", elapsed, requested)
}
