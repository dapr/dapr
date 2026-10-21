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

package orchestrator

import (
	"context"
	"time"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

// isFallback reports whether this daprd hosts the workflow actor type as a
// fallback host (Options.Fallback): it has no local executor, so a turn here
// fails at once and the instance advances only once its actor moves to a
// daprd with one. It changes while the factory is registered, as a daprd
// turns into a fallback host or back in place, so it is read where it is
// needed rather than once.
func (f *factory) isFallback() bool {
	return f.fallback != nil && f.fallback()
}

// localFastPath reports whether an operation served outside a turn may take
// the WorkflowsFastPath local drives and elisions. A fallback host takes the
// durable reminder path instead: a local drive runs a turn here, which fails,
// and an elided reminder would leave the operation to a janitor no turn has
// armed yet. Decisions made inside a turn keep using fastPath: a turn runs
// only on a daprd with a local executor.
func (f *factory) localFastPath() bool {
	return f.fastPath && !f.isFallback()
}

// armFallbackJanitor stands in on a fallback host for dispatching the
// activities a rerun schedules: no local executor can run them, and a
// fallback host does not advertise the activity actor type to the
// scheduler. It arms the instance's janitor due at once. The janitor fires
// on a daprd with an executor, which forwards it to the instance's owner
// until the instance moves to such a daprd, where its re-dispatch of the
// unresolved tasks sends the activities.
func (o *orchestrator) armFallbackJanitor(ctx context.Context, state *wfenginestate.State) error {
	wfName := o.getExecutionStartedEvent(state).GetName()
	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
		Name:      janitorReminderName,
		DueTime:   time.Now().UTC().Format(time.RFC3339Nano),
		Period:    janitorPeriod().String(),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Drop{
				Drop: new(commonv1pb.JobFailurePolicyDrop),
			},
		},
		ConcurrencyKey: &wfName,
	})
}

// fallbackOperation returns the diagnostics operation of a workflow actor
// method served on a fallback host. ev is the parsed AddWorkflowEvent event.
func fallbackOperation(method string, ev *backend.HistoryEvent) string {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return diag.CreateWorkflow
	case todo.PurgeWorkflowStateMethod, todo.RecursivePurgeWorkflowStateMethod:
		return diag.PurgeWorkflow
	case todo.RerunWorkflowInstance:
		return diag.RerunWorkflow
	case todo.ForkWorkflowHistory:
		return diag.ForkWorkflow
	case todo.AddWorkflowEventMethod:
	default:
		return ""
	}

	switch {
	case ev.GetEventRaised() != nil:
		return diag.RaiseEvent
	case ev.GetExecutionTerminated() != nil:
		return diag.TerminateWorkflow
	case ev.GetExecutionSuspended() != nil:
		return diag.SuspendWorkflow
	case ev.GetExecutionResumed() != nil:
		return diag.ResumeWorkflow
	default:
		return diag.AddEvent
	}
}

// fallbackServed records an operation served on a fallback host. Reads, such
// as a status query, are not operations on the instance and are not
// recorded. A purge completes at once, so it is not counted as staged.
func (f *factory) fallbackServed(ctx context.Context, method string, ev *backend.HistoryEvent) {
	operation := fallbackOperation(method, ev)
	if operation == "" {
		return
	}
	if operation != diag.PurgeWorkflow {
		diag.DefaultWorkflowMonitoring.WorkflowStagedOperation(ctx, operation)
	}
	if f.onFallback != nil {
		f.onFallback(operation)
	}
}
