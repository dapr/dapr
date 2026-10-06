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

package early

import (
	"context"
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
	suite.Register(new(passed))
}

// passed asserts that an activity result is not kept in the inbox once the
// generation has used its event ID for something other than a scheduling.
// IDs are assigned in sequence per generation, so a result for an ID the
// history has already passed can never be consumed. Here the ID is taken by
// a detached workflow creation, which is not a scheduling event: a keep rule
// that looked only at scheduling IDs would hold the result for the rest of
// the run.
type passed struct {
	workflow *workflow.Workflow
}

func (p *passed) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		// Signing mode opt-out: the injected unsigned inbox event would be rejected by signing verification.
		workflow.WithSigning(false),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *passed) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	// Sequence numbers: the WaitForSingleEvent synthetic timer takes id 0, so
	// the detached workflow creation is id 1.
	require.NoError(t, p.workflow.Registry().AddWorkflowN("dedup-early-passed", func(ctx *task.WorkflowContext) (any, error) {
		done := ctx.WaitForSingleEvent("done", time.Hour)
		if _, err := ctx.ScheduleNewDetachedWorkflow("dedup-early-passed-detached"); err != nil {
			return nil, err
		}
		if err := done.Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	}))
	require.NoError(t, p.workflow.Registry().AddWorkflowN("dedup-early-passed-detached", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	cl := p.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-early-passed")
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsDetachedCreatedFor(1)))
	}, 10*time.Second, 10*time.Millisecond)

	fworkflow.InjectInboxEvent(t, ctx, p.workflow.DB(), p.workflow.Dapr(), string(id), fworkflow.TaskCompletedEvent(1, `"unconsumable"`))

	p.workflow.Dapr().Restart(t, ctx)
	p.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = p.workflow.BackendClient(t, ctx)

	// Nudge a turn over the injected result; the workflow is not waiting
	// for this event.
	require.NoError(t, cl.RaiseEvent(ctx, id, "nudge"))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, fworkflow.IsEventRaisedFor("nudge")))
		assert.Zero(c, fworkflow.InboxLength(t, ctx, p.workflow.DB(), string(id)),
			"a result for an id the generation has passed must be dropped, not kept in the inbox")
	}, 10*time.Second, 10*time.Millisecond)

	require.NoError(t, cl.RaiseEvent(ctx, id, "done"))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String(), meta.GetFailureDetails().GetErrorMessage())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
}
