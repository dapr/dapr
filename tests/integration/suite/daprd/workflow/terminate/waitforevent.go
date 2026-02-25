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

package terminate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(waitforevent))
}

type waitforevent struct {
	workflow *workflow.Workflow
}

func (w *waitforevent) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *waitforevent) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	w.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.WaitForSingleEvent("bar", time.Minute).Await(nil))
		return nil, nil
	})

	cl := w.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := w.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs")
		if assert.Len(t, keys, 1) {
			assert.Contains(t, keys[0], "timer-0")
		}
	}, time.Second*10, 10*time.Millisecond)

	require.NoError(t, cl.TerminateOrchestration(ctx, id))

	meta, err := cl.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	require.Equal(t, "ORCHESTRATION_STATUS_TERMINATED", meta.RuntimeStatus.String())
}
