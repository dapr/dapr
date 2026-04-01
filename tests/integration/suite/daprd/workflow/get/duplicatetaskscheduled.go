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

package get

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
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(duplicatetaskscheduled))
}

// duplicatetaskscheduled verifies that a fan-out workflow (multiple activities
// scheduled in the same batch) does not produce duplicate TaskScheduled events
// in history after completion. The applier must skip adding a TaskScheduled
// event to NewEvents when OldEvents already contains one with the same
// EventId, otherwise each re-execution of the orchestrator appends duplicates.
type duplicatetaskscheduled struct {
	workflow   *workflow.Workflow
	remoteGate chan struct{}
}

func (d *duplicatetaskscheduled) Setup(t *testing.T) []framework.Option {
	d.remoteGate = make(chan struct{})
	d.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *duplicatetaskscheduled) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	reg1 := dworkflow.NewRegistry()
	reg2 := dworkflow.NewRegistry()

	reg1.AddWorkflowN("fanout", func(ctx *dworkflow.WorkflowContext) (any, error) {
		localTask := ctx.CallActivity("local")
		remoteTask := ctx.CallActivity("remote",
			dworkflow.WithActivityAppID(d.workflow.DaprN(1).AppID()))

		if err := localTask.Await(nil); err != nil {
			return nil, err
		}
		if err := remoteTask.Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	reg1.AddActivityN("local", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	var inRemote atomic.Bool
	reg2.AddActivityN("remote", func(ctx dworkflow.ActivityContext) (any, error) {
		inRemote.Store(true)
		select {
		case <-d.remoteGate:
		case <-ctx.Context().Done():
			return nil, ctx.Context().Err()
		}
		return nil, nil
	})

	wf1 := d.workflow.WorkflowClientN(t, ctx, 0)
	wf1.StartWorker(ctx, reg1)
	wf2 := d.workflow.WorkflowClientN(t, ctx, 1)
	wf2.StartWorker(ctx, reg2)

	id, err := wf1.ScheduleWorkflow(ctx, "fanout")
	require.NoError(t, err)

	require.Eventually(t, inRemote.Load, 5*time.Second, 10*time.Millisecond)
	close(d.remoteGate)

	_, err = wf1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	resp, err := wf1.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	seen := make(map[int32]int)
	for _, e := range resp.Events {
		if e.GetTaskScheduled() != nil {
			seen[e.GetEventId()]++
		}
	}

	require.Len(t, seen, 2, "expected exactly 2 distinct TaskScheduled events (local and remote)")
	for eventID, count := range seen {
		assert.Equalf(t, 1, count,
			"TaskScheduled with EventId %d appears %d times in history; expected exactly once", eventID, count)
	}
}
