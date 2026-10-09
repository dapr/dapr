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

package selecttask

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(decodeerrornoretry))
}

// decodeerrornoretry awaits a retry-wrapped activity whose successful result
// does not decode into the caller's type. The decode error is returned as is
// and never consumes a retry attempt.
type decodeerrornoretry struct {
	workflow *workflow.Workflow
	calls    atomic.Int64
}

func (d *decodeerrornoretry) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *decodeerrornoretry) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddWorkflowN("decodeerrornoretry", func(ctx *task.WorkflowContext) (any, error) {
		stringy := ctx.CallActivity("stringy", task.WithActivityRetryPolicy(&task.RetryPolicy{
			MaxAttempts:          3,
			InitialRetryInterval: time.Millisecond * 10,
		}))
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(stringy, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, errors.New("expected the activity to win")
		}
		var n int
		err = stringy.Await(&n)
		if err == nil {
			return nil, errors.New("expected a decode error")
		}
		return err.Error(), nil
	})
	d.workflow.Registry().AddActivityN("stringy", func(task.ActivityContext) (any, error) {
		d.calls.Add(1)
		return "not a number", nil
	})

	cl := d.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "decodeerrornoretry")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetOutput().GetValue(), "cannot unmarshal")
	assert.Equal(t, int64(1), d.calls.Load())

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assert.Len(t, fworkflow.ScheduledExecIDs(hist.GetEvents(), "stringy"), 1)
	assert.Empty(t, fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents()))
}
