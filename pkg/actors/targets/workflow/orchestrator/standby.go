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
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

const (
	// standbyWaitingWarnAfter is how long a standby host goes with no worker
	// connected before it warns about the operations it served, which wait
	// for one. The warnings then come at doubling intervals since the
	// standby registration, capped at standbyWaitingWarnMaxInterval.
	standbyWaitingWarnAfter       = 5 * time.Minute
	standbyWaitingWarnMaxInterval = time.Hour
)

// errStandbyTurn refuses a reminder on a standby host (Options.Standby): it
// would run a workflow turn, and no worker is connected to run it. A standby
// host does not advertise the workflow actor type to the scheduler, so a
// reminder reaches it only when another daprd forwards one through a stale
// placement table while a worker connects. FailedPrecondition fails the
// trigger, and the reminder's retry policy delivers it again once the table
// routes the actor to the daprd with the worker.
func errStandbyTurn(actorID, reminder string) error {
	return status.Errorf(codes.FailedPrecondition,
		"workflow actor '%s' is on a standby host with no workflow worker connected; not running reminder '%s'",
		actorID, reminder)
}

// armStandbyJanitor stands in on a standby host for dispatching the
// activities a rerun schedules: the standby activity actors refuse every
// call, as no worker is connected to run one. It arms the instance's janitor
// due at once. The standby host does not advertise the workflow actor type to
// the scheduler, so the janitor fires once a worker connects, on the daprd
// with the worker, where its re-dispatch of unresolved tasks sends the
// activities.
func (o *orchestrator) armStandbyJanitor(ctx context.Context, state *wfenginestate.State) error {
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

// standbyOperation returns the diagnostics operation of a workflow actor
// method served on a standby host. ev is the parsed AddWorkflowEvent event.
func standbyOperation(method string, ev *backend.HistoryEvent) string {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		return diag.CreateWorkflow
	case todo.PurgeWorkflowStateMethod, todo.RecursivePurgeWorkflowStateMethod:
		return diag.PurgeWorkflow
	case todo.RerunWorkflowInstance:
		return diag.RerunWorkflow
	case todo.ForkWorkflowHistory:
		return diag.ForkWorkflow
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

// standbyServed records a workflow actor method served on a standby host.
func (f *factory) standbyServed(ctx context.Context, method string, ev *backend.HistoryEvent) {
	operation := standbyOperation(method, ev)
	diag.DefaultWorkflowMonitoring.WorkflowStandbyOperation(ctx, operation)
	f.standbyWaiting.served(operation)
}

// standbyWaiting warns on a standby host when the operations it served keep
// waiting for a workflow worker to connect to the app: a start, event, rerun
// or fork does not advance until one does. Its run stops with the standby
// registration, which ends once a worker connects to this daprd.
type standbyWaiting struct {
	appID   string
	clock   clock.Clock
	waiting atomic.Int64
	// warn logs the warning; a field so unit tests can capture it.
	warn func(appID string, waiting int64, since time.Duration)
}

func newStandbyWaiting(appID string) *standbyWaiting {
	return &standbyWaiting{
		appID: appID,
		clock: clock.RealClock{},
		warn: func(appID string, waiting int64, since time.Duration) {
			log.Warnf("%d workflow operations served on this standby host are waiting for a workflow worker to connect to app '%s'; no worker has connected for %s",
				waiting, appID, since)
		},
	}
}

// served counts operation as waiting for a worker, unless it is a purge,
// which a standby host completes.
func (w *standbyWaiting) served(operation string) {
	if operation != diag.PurgeWorkflow {
		w.waiting.Add(1)
	}
}

// run warns at standbyWaitingWarnAfter and at doubling intervals after it,
// while any served operation is waiting, until ctx is done.
func (w *standbyWaiting) run(ctx context.Context) {
	start := w.clock.Now()
	next := standbyWaitingWarnAfter
	for {
		t := w.clock.NewTimer(start.Add(next).Sub(w.clock.Now()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}

		if n := w.waiting.Load(); n > 0 {
			w.warn(w.appID, n, next)
		}
		next += min(next, standbyWaitingWarnMaxInterval)
	}
}
