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

package scheduler

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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(killrestart))
}

// killrestart kills the scheduler with SIGKILL while an activity runs, starts
// it again, and expects the workflow to complete within a few seconds. A
// killed scheduler cannot revoke its leadership lease, so its leadership key
// stays in etcd until the lease expires. The restarted scheduler must not
// wait for that expiry (about 21s) before it serves jobs again.
type killrestart struct {
	workflow *workflow.Workflow
}

func (k *killrestart) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *killrestart) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	var started atomic.Int32
	holdCh := make(chan struct{})

	require.NoError(t, k.workflow.Registry().AddWorkflowN("wf",
		func(ctx *task.WorkflowContext) (any, error) {
			var out string
			err := ctx.CallActivity("act").Await(&out)
			return out, err
		}))
	require.NoError(t, k.workflow.Registry().AddActivityN("act",
		func(ctx task.ActivityContext) (any, error) {
			started.Add(1)
			select {
			case <-holdCh:
			case <-ctx.Context().Done():
				return nil, ctx.Context().Err()
			}
			return "done", nil
		}))

	cl := k.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, started.Load(), int32(1))
	}, 20*time.Second, 10*time.Millisecond)

	// Restart sends SIGKILL, so the leadership key and its lease stay in
	// etcd.
	k.workflow.Scheduler().Restart(t, ctx)
	restarted := time.Now()
	close(holdCh)

	// Shorter than the 45s case timeout of the framework, so a workflow that
	// never completes fails here with a clear error.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	elapsed := time.Since(restarted)
	t.Logf("workflow completed %s after the scheduler restart", elapsed)

	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	assert.Less(t, elapsed, 10*time.Second,
		"the restarted scheduler must not wait for the lease of its killed predecessor to expire")
}
