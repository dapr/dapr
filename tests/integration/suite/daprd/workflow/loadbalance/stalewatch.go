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

package loadbalance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stalewatch))
}

// stalewatch makes every completion wait use the watch-stream fallback and
// re-delivers every workflow-turn completion. A turn's re-delivered completion
// parks on the executor actor while the next activity runs, so the next
// turn's watch stream serves it first. durabletask discards it as stale and
// keeps the registration armed. The waiter must then open another watch
// stream for the genuine completion, or the workflow stops.
type stalewatch struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (s *stalewatch) Setup(t *testing.T) []framework.Option {
	s.logline = logline.New(t, logline.WithCaptureAll())
	s.workflow = workflow.NewClustered(t, 1, daprd.WithExecOptions(
		exec.WithEnvVars(t,
			"DAPR_WORKFLOW_TEST_FORCE_WATCH_FALLBACK", "1000000",
			"DAPR_WORKFLOW_TEST_DUPLICATE_TURN_COMPLETIONS", "1000000",
		),
		exec.WithStdout(s.logline.Stdout()),
		exec.WithStderr(s.logline.Stderr()),
	))

	return []framework.Option{
		framework.WithProcesses(s.logline, s.workflow),
	}
}

func (s *stalewatch) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	reg := s.workflow.RegistryN(0)
	require.NoError(t, reg.AddWorkflowN("seq", func(ctx *task.WorkflowContext) (any, error) {
		for range 4 {
			if err := ctx.CallActivity("echo").Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	// The re-delivery comes 150ms after the turn completion. The activity
	// outlasts it, so the copy parks before the next turn starts to wait.
	require.NoError(t, reg.AddActivityN("echo", func(task.ActivityContext) (any, error) {
		time.Sleep(time.Millisecond * 300)
		return nil, nil
	}))

	cl := s.workflow.BackendClientN(t, ctx, 0)

	ids := make([]api.InstanceID, 5)
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("stalewatch-%d", i))
		_, err := cl.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}

	wctx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()
	fworkflow.WaitForAllCompleted(t, wctx, cl, ids...)

	// The completions alone would also pass if no stale copy reached a watch
	// stream any more, for example when a slow host shifts the 150ms
	// re-delivery relative to the activity. This pins that the re-watch path
	// ran.
	assert.Positive(t, s.logline.Count("discarding stale workflow task response"),
		"no stale copy reached a watch stream")
}
