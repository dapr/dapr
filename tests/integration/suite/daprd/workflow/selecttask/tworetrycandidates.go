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
	suite.Register(new(tworetrycandidates))
}

// tworetrycandidates Selects over two retry-wrapped activities that both fail
// every attempt, looping until both chains are exhausted. Both chains arm
// backoff timers from inside the same Select, in argument order, and the
// workflow replays each turn without a sequence mismatch.
type tworetrycandidates struct {
	workflow *workflow.Workflow
	callsA   atomic.Int64
	callsB   atomic.Int64
}

func (w *tworetrycandidates) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *tworetrycandidates) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	policy := &task.RetryPolicy{
		MaxAttempts:          3,
		InitialRetryInterval: time.Millisecond * 10,
	}
	w.workflow.Registry().AddWorkflowN("tworetrycandidates", func(ctx *task.WorkflowContext) (any, error) {
		pending := []task.Task{
			ctx.CallActivity("fail-a", task.WithActivityRetryPolicy(policy)),
			ctx.CallActivity("fail-b", task.WithActivityRetryPolicy(policy)),
		}

		var failures []string
		for len(pending) > 0 {
			winner, err := ctx.Select(pending...)
			if err != nil {
				return nil, err
			}
			err = pending[winner].Await(nil)
			if err == nil {
				return nil, errors.New("expected every chain to fail")
			}
			failures = append(failures, err.Error())
			pending = append(pending[:winner], pending[winner+1:]...)
		}
		return failures, nil
	})
	w.workflow.Registry().AddActivityN("fail-a", func(task.ActivityContext) (any, error) {
		w.callsA.Add(1)
		return nil, errors.New("fail-a failed")
	})
	w.workflow.Registry().AddActivityN("fail-b", func(task.ActivityContext) (any, error) {
		w.callsB.Add(1)
		return nil, errors.New("fail-b failed")
	})

	cl := w.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "tworetrycandidates")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetOutput().GetValue(), "fail-a failed")
	assert.Contains(t, meta.GetOutput().GetValue(), "fail-b failed")
	assert.Equal(t, int64(3), w.callsA.Load())
	assert.Equal(t, int64(3), w.callsB.Load())

	hist, err := cl.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	idsA := fworkflow.ScheduledExecIDs(hist.GetEvents(), "fail-a")
	idsB := fworkflow.ScheduledExecIDs(hist.GetEvents(), "fail-b")
	require.Len(t, idsA, 3)
	require.Len(t, idsB, 3)
	assert.Equal(t, idsA[0], idsA[1])
	assert.Equal(t, idsA[0], idsA[2])
	assert.Equal(t, idsB[0], idsB[1])
	assert.Equal(t, idsB[0], idsB[2])
	assert.NotEqual(t, idsA[0], idsB[0])

	// Each chain armed two backoff timers, each carrying its own chain's id.
	timers := fworkflow.ActivityRetryTimerExecIDs(hist.GetEvents())
	assert.Len(t, timers, 4)
	var forA, forB int
	for _, id := range timers {
		switch id {
		case idsA[0]:
			forA++
		case idsB[0]:
			forB++
		}
	}
	assert.Equal(t, 2, forA)
	assert.Equal(t, 2, forB)
}
