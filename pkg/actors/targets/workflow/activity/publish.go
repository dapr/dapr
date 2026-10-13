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

package activity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

// newPublishGrace returns the context that bounds every result publish of a
// factory (see publishAndSettle). It ends grace after runCtx ends, and
// nothing else ends it. grace is the runtime's graceful shutdown duration.
//
// This is the only time bound that this package puts on a publish. The
// publish waits for the parent workflow's turn lock, and in a fan-out every
// completion queues on that one lock behind the turn that scheduled the
// activities, so the wait grows with the fan-out. The wait is backpressure,
// not a failure. A deadline on it settled the execution as failed, the
// run-activity reminder ran the body again, and the re-run queued on the same
// lock.
func newPublishGrace(runCtx context.Context, grace time.Duration) context.Context {
	ctx, cancel := context.WithCancel(context.WithoutCancel(runCtx))
	context.AfterFunc(runCtx, func() {
		time.AfterFunc(grace, cancel)
	})
	return ctx
}

// errPublishAbandoned settles an execution whose result can no longer be
// published because the runtime is shutting down. Recoverable: followers
// parked on the call surface it into their retry chains instead of waiting on
// a watcher that will never report.
var errPublishAbandoned = wferrors.NewRecoverable(errors.New(
	"activity result publish abandoned at shutdown; the work item is re-dispatched on the next owner"))

// watchAndPublish hands an execution whose owner's ctx cancelled to the
// factory's runtime-lifetime runner, so the WorkItem already in the
// durabletask queue still has its result published and its inflight entry
// finalised. It lives on the factory, never invoking the activity actor, so
// recycling or rebalancing the actor cannot affect it. The runner both
// accounts for the goroutine (nothing publishes into a parent workflow after
// the runtime has drained) and bounds it: a callback that never arrives is
// abandoned at shutdown rather than parking forever.
func (f *factory) watchAndPublish(origCtx context.Context, ex *execution) {
	started := f.detached.Go(func(ctx context.Context) {
		select {
		case completed := <-ex.callback:
			// There is no caller left to return the outcome to: it is
			// recorded in metrics and carried on the settled call, which is
			// what parked followers read.
			_ = f.publishAndSettle(context.WithoutCancel(origCtx), ex, completed)
		case <-ctx.Done():
			// The callback can be ready too, and select picks a ready case at
			// random. A result already in hand goes through the shutdown
			// grace like any other.
			select {
			case completed := <-ex.callback:
				_ = f.publishAndSettle(context.WithoutCancel(origCtx), ex, completed)
			default:
				f.abandonPublish(ex)
			}
		}
	})
	if !started {
		f.abandonPublish(ex)
	}
}

// cancelBeforePublishForTest: the first N times an SDK callback arrives, the
// invocation context is cancelled before the result is published, making the
// placement-drain race deterministic. Execution stays at-least-once either
// way; the point is that a result already in hand is not thrown away.
var cancelBeforePublishForTest = testBudget("DAPR_WORKFLOW_TEST_CANCEL_BEFORE_PUBLISH")

// abandonPublish gives up on an execution's result at shutdown: the outcome
// dies with this host and the parent workflow's janitor re-dispatches the
// unresolved task on the next owner. Settling unblocks parked followers into
// their retry chains rather than leaving them on a call nothing will report.
func (f *factory) abandonPublish(ex *execution) {
	log.Warnf("Activity actor '%s': abandoning the result publish to workflow '%s' at shutdown; the work item is re-dispatched on the next owner", ex.actorID, ex.workflowID)
	ex.unregister()
	f.settle(ex.key, ex.call, errPublishAbandoned)
}

// publishOwned delivers the outcome an owner holds in hand. The publish must
// not depend on the liveness of the call that happened to be waiting on it:
// that ctx is cut by client disconnect, invocation timeout and placement
// drain, none of which lose the result, and re-running the body for it is
// avoidable waste (the orchestrator dedups the duplicate, but the app observes
// every extra run). So it runs on the runtime-lifetime runner with a context
// of its own, like watchAndPublish.
func (f *factory) publishOwned(ctx context.Context, ex *execution, completed bool) error {
	done := make(chan error, 1)
	started := f.detached.Go(func(context.Context) {
		done <- f.publishAndSettle(context.WithoutCancel(ctx), ex, completed)
	})
	if !started {
		f.abandonPublish(ex)
		return errPublishAbandoned
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// publishAndSettle posts one activity outcome back to the parent workflow and
// finalises its inflight entry. Both publishOwned and watchAndPublish call it
// on the runtime-lifetime runner, with base set to the caller's context minus
// cancellation; nothing else differs between the two.
//
// The publish runs on base, which has no deadline: it waits for a busy parent
// for as long as the parent takes. Only the factory's publish grace (see
// newPublishGrace) cancels it.
//
// At shutdown, the runner's context ends and the actor router's Run cancels
// every router call in flight. So an AddWorkflowEvent call that waits for the
// parent ends at once. The publish context stays live for the graceful
// shutdown duration after that. With WorkflowsRemoteActivityReminder off, the
// publish then settles at once with the recoverable "failed to invoke" error.
// With it on (the default), publishResult creates a durable result reminder
// for the parent on the publish context, and the grace bounds that create
// (and the placement lookup before the call):
//   - The scheduler accepts the reminder: the result is not lost, and the
//     publish settles as a success.
//   - The create fails within the grace: the publish settles with the
//     recoverable "failed to invoke" error.
//   - The create still retries when the grace ends: the publish is abandoned
//     (see abandonPublish).
func (f *factory) publishAndSettle(base context.Context, ex *execution, completed bool) error {
	// A lost race here means an eviction already finished the call and a
	// fresh execution is running; publish anyway, the orchestrator's
	// duplicate-completion dedup absorbs whichever copy arrives second.
	_ = ex.call.BeginResolve()
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	stop := context.AfterFunc(f.publishGrace, cancel)
	defer stop()
	execErr := f.publishResult(ctx, ex, completed)
	// base never cancels, so a cancelled ctx means the shutdown grace ended
	// the publish.
	if execErr != nil && ctx.Err() != nil {
		f.abandonPublish(ex)
		return errPublishAbandoned
	}
	f.settle(ex.key, ex.call, execErr)
	ex.unregister()
	return execErr
}

// publishResult handles everything after the SDK callback has fired: it
// validates the result, attaches signing attestations, and posts the
// completion event back to the workflow actor. Lives on factory because
// it must remain safe to invoke from a background goroutine after the
// owning *activity may have been recycled.
func (f *factory) publishResult(ctx context.Context, ex *execution, completed bool) error {
	executionStatus := ""
	elapsed := diag.ElapsedSince(ex.start)
	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.ActivityExecutionEvent(ctx, ex.activityName, executionStatus, elapsed)
		}
	}()

	if !completed {
		// Activity execution failed with recoverable error: AbandonActivityWorkItem was called.
		executionStatus = diag.StatusRecoverable
		return wferrors.NewRecoverable(todo.ErrExecutionAborted)
	}
	log.Debugf("Activity actor '%s': activity completed for workflow with instanceId '%s' activityName '%s'", ex.actorID, ex.wi.InstanceID, activityReminderName)

	if err := f.attachAttestation(ctx, ex); err != nil {
		executionStatus = diag.StatusRecoverable
		return err
	}

	// send completed event to orchestrator wf actor
	wfActorType := f.workflowActorType
	if router := ex.wi.NewEvent.GetRouter(); router != nil {
		wfActorType = f.actorTypeBuilder.Workflow(router.GetSourceAppID())
	}

	var err error
	// TODO: @joshvanl: remove `workflowsRemoteActivityReminder` check in later
	// version.
	if f.workflowsRemoteActivityReminder && f.actorNotReachable(ctx, wfActorType, ex.workflowID) {
		err = f.createWorkflowResultReminder(ctx, wfActorType, ex.workflowID, ex.parentExecutionID, ex.wi.Result)
	} else {
		// publish the result back to the workflow actor as a new event to be processed
		for {
			var resultData []byte
			resultData, err = proto.Marshal(ex.wi.Result)
			if err != nil {
				// Returning non-recoverable error
				executionStatus = diag.StatusFailed
				return err
			}

			req := internalsv1pb.
				NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
				WithActor(wfActorType, ex.workflowID).
				WithData(resultData).
				WithContentType(invokev1.ProtobufContentType)
			if ex.parentExecutionID != "" {
				req = req.WithMetadata(map[string][]string{todo.MetadataParentExecutionID: {ex.parentExecutionID}})
			}
			_, err = f.router.Call(ctx, req)
			if !f.signAgain(ctx, ex, err) {
				break
			}
		}
	}

	switch {
	case err != nil:
		if strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
			log.Errorf("Activity actor '%s': workflow actor instance not found when reporting activity result for workflow with instanceId '%s': %s", ex.actorID, ex.wi.InstanceID, err)
			executionStatus = diag.StatusFailed
			return nil
		}

		// The reminder can wait for the parent for a long time, so it holds
		// the result signed again: the parent then has the full life of the
		// current certificate. A result that the parent refused as expired
		// goes back to the run-activity reminder: signAgain got no newer
		// certificate for it.
		if f.workflowsRemoteActivityReminder && !signing.IsSignerCertExpired(err) && f.attachAttestation(ctx, ex) == nil {
			if cerr := f.createWorkflowResultReminder(ctx, wfActorType, ex.workflowID, ex.parentExecutionID, ex.wi.Result); cerr == nil {
				return nil
			}
		}

		// Returning recoverable error, record metrics
		executionStatus = diag.StatusRecoverable
		return wferrors.NewRecoverable(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", todo.AddWorkflowEventMethod, err))
	case ex.wi.Result.GetTaskCompleted() != nil:
		// Activity execution completed successfully
		executionStatus = diag.StatusSuccess
	case ex.wi.Result.GetTaskFailed() != nil:
		// Activity execution failed
		executionStatus = diag.StatusFailed
	}

	return nil
}

// attachAttestation signs the result, so the parent workflow can
// cryptographically verify this activity's identity, input, and output. It
// replaces an earlier signature. No-op when signing is disabled
// (AttachActivityCompletionAttestation handles the nil-Signer case
// internally).
func (f *factory) attachAttestation(ctx context.Context, ex *execution) error {
	if ex.wi.Result == nil {
		return nil
	}
	scheduled := ex.wi.NewEvent.GetTaskScheduled()
	if scheduled == nil {
		return wferrors.NewRecoverable(fmt.Errorf("activity actor '%s': cannot build activity attestation without TaskScheduledEvent", ex.actorID))
	}
	if err := f.signing.AttachActivityCompletionAttestation(ctx, ex.wi.Result, signing.ActivityAttestationParams{
		ParentInstanceID: ex.workflowID,
		ActivityName:     ex.activityName,
		Input:            scheduled.GetInput(),
	}); err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("activity actor '%s': %w", ex.actorID, err))
	}
	return nil
}

// signAgain signs the result again after the parent refused it, because the
// signer certificate expired while the result waited for the parent. It
// reports whether the new signature uses a newer certificate: only then can a
// delivery again succeed.
func (f *factory) signAgain(ctx context.Context, ex *execution, err error) bool {
	if !signing.IsSignerCertExpired(err) {
		return false
	}
	refused := signerCertificate(ex.wi.Result)
	if f.attachAttestation(ctx, ex) != nil || bytes.Equal(refused, signerCertificate(ex.wi.Result)) {
		return false
	}
	log.Infof("Activity actor '%s': workflow '%s' refused the result because its signer certificate expired; delivering it again with the current certificate", ex.actorID, ex.workflowID)
	return true
}

// signerCertificate returns the certificate chain that signed the result.
func signerCertificate(e *backend.HistoryEvent) []byte {
	if c := e.GetTaskCompleted(); c != nil {
		return c.GetSignerCertificate()
	}
	return e.GetTaskFailed().GetSignerCertificate()
}

func (f *factory) actorNotReachable(ctx context.Context, wfActorType, workflowID string) bool {
	_, _, cancel, err := f.placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: wfActorType,
		ActorID:   workflowID,
	})
	if cancel != nil {
		cancel(nil)
	}
	return errors.Is(err, messages.ErrActorNoAddress)
}
