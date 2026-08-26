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

package wakev2

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reconnect))
}

// reconnect asserts that a workflow whose FIRST turn is in-flight when its
// worker disconnects is re-driven after the worker reconnects. The first turn
// never commits, so the per-instance janitor is not yet armed: recovery must
// come from the durable start-es reminder surviving the listener churn.
// This test is not sensitive to the janitor period, but it is set short so a
// janitor-based recovery would also be observed within the completion window.
type reconnect struct {
	workflow *workflow.Workflow
	called   atomic.Int64
	waitCh   chan struct{}
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	r.waitCh = make(chan struct{})
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
			// WithWorkflowJanitorPeriod does not exist on this branch's
			// framework, so set the env var directly.
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("foo", func(c *task.WorkflowContext) (any, error) {
		r.called.Add(1)
		<-r.waitCh
		return nil, c.CallActivity("bar").Await(nil)
	}))
	require.NoError(t, r.workflow.Registry().AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "", nil
	}))

	cctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	client := r.workflow.BackendClient(t, cctx)

	// A start time of now arms the durable start-es reminder with a dueTime
	// that is already in the past by the time the scheduler processes it.
	id, err := client.ScheduleNewWorkflow(ctx, "foo", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// The first turn is now in-flight, blocked in the workflow body.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.called.Load())
	}, time.Second*10, time.Millisecond*10)

	// Cancel the listener while the first turn is held: actor types
	// unregister and the in-flight turn aborts without committing.
	cancel()
	r.workflow.WaitForNoConnectedWorkers(t, ctx)
	close(r.waitCh)

	cctx, cancel = context.WithCancel(ctx)
	t.Cleanup(cancel)
	client = r.workflow.BackendClient(t, cctx)

	wctx, wcancel := context.WithTimeout(ctx, time.Second*10)
	t.Cleanup(wcancel)
	meta, err := client.WaitForWorkflowCompletion(wctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))

	assert.Equal(t, int64(3), r.called.Load())
}
