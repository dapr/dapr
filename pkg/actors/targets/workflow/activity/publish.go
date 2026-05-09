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
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

// detachedPublishTimeout bounds how long publishResult waits when the caller
// ctx was already canceled before the SDK callback fired.
// context.WithoutCancel strips the original deadline, so we apply a fresh one
// to keep a misbehaving downstream from blocking the actor lock indefinitely.
const detachedPublishTimeout = 30 * time.Second

// watchAndPublish runs on the factory (not the activity) so it cannot be
// affected by the activity actor being recycled or rebalanced after the
// caller's ctx canceled. All state it needs is captured by argument or read
// from factory-level fields that are immutable post-init. Crucially it does
// not invoke any method on the activity actor itself, so the actor's
// turn-based contract is preserved.
//
// origCtx is the (already-cancelled) ctx from the owner; we strip
// cancellation but keep trace context and values via context.WithoutCancel,
// then apply a fresh detachedPublishTimeout deadline so a misbehaving
// downstream cannot block this goroutine indefinitely.
func (f *factory) watchAndPublish(origCtx context.Context, actorID, key string, call *inflight.Call, callback chan bool, wi *backend.ActivityWorkItem, taskEvent *backend.HistoryEvent, name, activityName, workflowID string, start time.Time) {
	completed := <-callback
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(origCtx), detachedPublishTimeout)
	defer cancel()
	execErr := f.publishResult(pubCtx, actorID, completed, wi, taskEvent, name, activityName, workflowID, start)
	call.Finish(execErr)
	// Cache the outcome for follower retries only on success; on error,
	// release immediately so subsequent cron retries become fresh owners
	// and can re-attempt rather than seeing the cached failure for the
	// full TTL window.
	if execErr == nil {
		f.inflight.ReleaseAfter(key, call, inflightCacheTTL)
	} else {
		f.inflight.Release(key, call)
	}
}

// publishResult handles everything after the SDK callback has fired: it
// validates the result, attaches signing attestations, and posts the
// completion event back to the workflow actor. Lives on factory because
// it must remain safe to invoke from a background goroutine after the
// owning *activity may have been recycled.
func (f *factory) publishResult(ctx context.Context, actorID string, completed bool, wi *backend.ActivityWorkItem, taskEvent *backend.HistoryEvent, name, activityName, workflowID string, start time.Time) error {
	executionStatus := ""
	elapsed := diag.ElapsedSince(start)
	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.ActivityExecutionEvent(ctx, activityName, executionStatus, elapsed)
		}
	}()

	if !completed {
		// Activity execution failed with recoverable error: AbandonActivityWorkItem was called.
		executionStatus = diag.StatusRecoverable
		return wferrors.NewRecoverable(todo.ErrExecutionAborted)
	}
	log.Debugf("Activity actor '%s': activity completed for workflow with instanceId '%s' activityName '%s'", actorID, wi.InstanceID, name)

	// Attach an attestation so the parent workflow can cryptographically
	// verify this activity's identity, input, and output. No-op when
	// signing is disabled (AttachActivityCompletionAttestation handles
	// the nil-Signer case internally).
	if wi.Result != nil {
		scheduled := taskEvent.GetTaskScheduled()
		if scheduled == nil {
			executionStatus = diag.StatusRecoverable
			return wferrors.NewRecoverable(fmt.Errorf("activity actor '%s': cannot build activity attestation without TaskScheduledEvent", actorID))
		}
		if attachErr := f.signing.AttachActivityCompletionAttestation(ctx, wi.Result, signing.ActivityAttestationParams{
			ParentInstanceID: workflowID,
			ActivityName:     activityName,
			Input:            scheduled.GetInput(),
		}); attachErr != nil {
			executionStatus = diag.StatusRecoverable
			return wferrors.NewRecoverable(fmt.Errorf("activity actor '%s': %w", actorID, attachErr))
		}
	}

	// send completed event to orchestrator wf actor
	wfActorType := f.workflowActorType
	if router := taskEvent.GetRouter(); router != nil {
		wfActorType = f.actorTypeBuilder.Workflow(router.GetSourceAppID())
	}

	var err error
	// TODO: @joshvanl: remove `workflowsRemoteActivityReminder` check in later
	// version.
	if f.workflowsRemoteActivityReminder && f.actorNotReachable(ctx, wfActorType, workflowID) {
		err = f.createWorkflowResultReminder(ctx, wfActorType, workflowID, wi.Result)
	} else {
		// publish the result back to the workflow actor as a new event to be processed
		var resultData []byte
		resultData, err = proto.Marshal(wi.Result)
		if err != nil {
			// Returning non-recoverable error
			executionStatus = diag.StatusFailed
			return err
		}

		req := internalsv1pb.
			NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
			WithActor(wfActorType, workflowID).
			WithData(resultData).
			WithContentType(invokev1.ProtobufContentType)
		_, err = f.router.Call(ctx, req)
	}

	switch {
	case err != nil:
		if strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
			log.Errorf("Activity actor '%s': workflow actor instance not found when reporting activity result for workflow with instanceId '%s': %s", actorID, wi.InstanceID, err)
			executionStatus = diag.StatusFailed
			return nil
		}

		if f.workflowsRemoteActivityReminder {
			if cerr := f.createWorkflowResultReminder(ctx, wfActorType, workflowID, wi.Result); cerr == nil {
				return nil
			}
		}

		// Returning recoverable error, record metrics
		executionStatus = diag.StatusRecoverable
		return wferrors.NewRecoverable(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", todo.AddWorkflowEventMethod, err))
	case wi.Result.GetTaskCompleted() != nil:
		// Activity execution completed successfully
		executionStatus = diag.StatusSuccess
	case wi.Result.GetTaskFailed() != nil:
		// Activity execution failed
		executionStatus = diag.StatusFailed
	}

	return nil
}
