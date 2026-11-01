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

package keptinbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(mixed))
}

// mixed asserts that the janitor still runs a turn when the inbox
// holds an early result next to an event the turn can consume.
type mixed struct {
	workflow *workflow.Workflow
}

func (k *mixed) Setup(t *testing.T) []framework.Option {
	k.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		// The injected unsigned result would be rejected by signing verification.
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *mixed) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	// Echo takes id 0, the WaitForSingleEvent synthetic timer id 1.
	require.NoError(t, k.workflow.Registry().AddWorkflowN("keptinboxmixed", func(c *task.WorkflowContext) (any, error) {
		if err := c.CallActivity("Echo").Await(nil); err != nil {
			return nil, err
		}
		if err := c.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	}))
	require.NoError(t, k.workflow.Registry().AddActivityN("Echo", func(task.ActivityContext) (any, error) {
		return "ok", nil
	}))

	cl := k.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "keptinboxmixed")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsTimerCreatedFor(1)))
		assert.Positive(c, k.workflow.Scheduler().JobKeyCount(t, ctx, "new-event-janitor"), "Echo's dispatch arms the janitor")
	}, 10*time.Second, 10*time.Millisecond)

	// Planted directly in the store, the event has no reminder or local
	// drive, so only the janitor can run the turn that consumes it.
	fworkflow.InjectInboxEvent(t, ctx, k.workflow.DB(), k.workflow.Dapr(), string(id), fworkflow.TaskCompletedEvent(50, `"held"`))
	fworkflow.InjectInboxEvent(t, ctx, k.workflow.DB(), k.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "go"}},
	})
	k.workflow.Dapr().Restart(t, ctx)
	k.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = k.workflow.BackendClient(t, ctx)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "the janitor treated an inbox with a consumable event as empty")
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	assert.Positive(t, k.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:janitor_recovered"))
}
