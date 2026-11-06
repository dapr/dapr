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

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

// fallbackNow reports whether this daprd hosts the workflow actor type as a
// fallback host (Options.Fallback): it has no local executor, so a turn here
// fails at once and the instance advances only once its actor moves to a
// daprd with one. It changes while the factory is registered, as a daprd
// turns into a fallback host or back in place, so it is read where it is
// needed rather than once.
func (f *factory) fallbackNow() bool {
	return f.fallback != nil && f.fallback()
}

// isFallback reports whether this daprd is a fallback host. While an invoked
// method runs it returns the value handleInvoke read at the start of the call
// (see pinFallback), so the path the method takes and the fallback header on
// its response agree even when a worker connects or disconnects meanwhile.
func (o *orchestrator) isFallback() bool {
	if pinned := o.pinnedFallback.Load(); pinned != nil {
		return *pinned
	}
	return o.fallbackNow()
}

// pinFallback reads the fallback state once and holds it for the rest of an
// invoked method, until the returned unpin is called.
func (o *orchestrator) pinFallback() (fallback bool, unpin func()) {
	fallback = o.fallbackNow()
	o.pinnedFallback.Store(&fallback)
	return fallback, func() { o.pinnedFallback.Store(nil) }
}

// dropFallbackCache checks the cached state against the store's metadata
// row at the start of a call on a fallback host, and drops it when the row no
// longer describes it, so the call reads the instance from the store. A force
// purge served by another daprd deletes the rows and evicts this daprd's
// actor, but when that eviction does not land (an owner that predates it, or
// a turn that held the lock past the purge's budget) the actor would go on
// serving the purged instance, refusing a start of its ID as already
// existing. The check is one Get, not a reload of the history. A cache the
// store has never served an ETag for cannot be checked, and is dropped. The
// caller holds the turn lock.
func (o *orchestrator) dropFallbackCache(ctx context.Context) {
	if !o.isFallback() {
		return
	}
	if o.state != nil && o.state.MetadataETagSeen() {
		if row, err := o.probeMetadataRow(ctx, o.state); err == nil && row == metadataRowMatches {
			return
		}
	}
	o.invalidateCachedState()
}

// localFastPath reports whether an operation served outside a turn may take
// the WorkflowsFastPath local drives and elisions. A fallback host takes the
// durable reminder path instead: a local drive runs a turn here, which fails,
// and an elided reminder would leave the operation to a janitor no turn has
// armed yet. Decisions made inside a turn keep using fastPath: a turn runs
// only on a daprd with a local executor.
func (o *orchestrator) localFastPath() bool {
	return o.fastPath && !o.isFallback()
}

// fallbackOperation returns the diagnostics operation of a workflow actor
// method served on a fallback host. create is the parsed
// CreateWorkflowInstance request, and ev the parsed AddWorkflowEvent event.
func fallbackOperation(method string, create *backend.CreateWorkflowInstanceRequest, ev *backend.HistoryEvent) string {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		// A parent workflow starts its child workflows with this method too.
		// A child's start comes from the parent's progress, not from a user.
		if create.GetStartEvent().GetExecutionStarted().GetParentInstance() != nil {
			return ""
		}
		return diag.CreateWorkflow
	case todo.RerunWorkflowInstance:
		// A rerun forks the source instance's history (ForkWorkflowHistory,
		// not counted) into a RerunWorkflowInstance on the new instance:
		// counting both would count one rerun twice.
		return diag.RerunWorkflow
	case todo.AddWorkflowEventMethod:
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
			// The other events come from a workflow's own progress, such as
			// a completion from a child or an activity on another app, not
			// from an operation a user made.
			return ""
		}
	default:
		return ""
	}
}

// fallbackServed records an operation staged on a fallback host. Reads, such
// as a status query, are not operations on the instance, and a purge
// completes at once, so neither is recorded.
func (f *factory) fallbackServed(ctx context.Context, method string, create *backend.CreateWorkflowInstanceRequest, ev *backend.HistoryEvent) {
	if operation := fallbackOperation(method, create, ev); operation != "" {
		diag.DefaultWorkflowMonitoring.WorkflowStagedOperation(ctx, operation)
	}
}
