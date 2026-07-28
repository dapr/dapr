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

package executor

import (
	"context"
	"strings"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// MetadataForwarded marks a Complete call that was already forwarded from a
// sibling-format rendezvous actor, so it is never forwarded again.
const MetadataForwarded = "forwarded"

const (
	// MetadataTaskType carries the task type of a Complete/Cancel call so
	// the receiving executor actor can deliver into the correctly
	// namespaced pending entry. Calls from pre-upgrade daprds lack it; the
	// task type is then inferred from the rendezvous key shape.
	MetadataTaskType = "tasktype"

	TaskTypeActivity = "activity"
	TaskTypeWorkflow = "workflow"
)

// PendingKey namespaces a rendezvous key by task type. The executor actor ID
// space is shared between workflow tasks (bare instance ID) and activity
// tasks (the activity actor ID "<instanceID>::<taskID>"), and instance IDs
// created through the TaskHub API may themselves contain "::": a workflow
// with instance ID "abc::5" and the activity with task ID 5 of a workflow
// "abc" share the executor actor ID "abc::5". Namespacing the pending map
// keeps their waiters and completions apart; the shared executor actor
// instance is harmless as it only ferries typed deliveries.
func PendingKey(taskType, key string) string {
	return taskType + "|" + key
}

// taskTypeOf resolves the task type of a Complete/Cancel call: from request
// metadata when present, otherwise inferred from the rendezvous key shape.
// Only pre-upgrade daprds omit the metadata, and their activity keys are
// always "<instanceID>/<taskID>" shaped, which no workflow instance ID can
// be (the scheduler rejects job names containing "/", so such a workflow
// could never have been created).
func taskTypeOf(req *internalsv1pb.InternalInvokeRequest, actorID string) string {
	if v, ok := req.GetMetadata()[MetadataTaskType]; ok && len(v.GetValues()) > 0 {
		return v.GetValues()[0]
	}
	if i := strings.LastIndex(actorID, "/"); i > 0 && isTaskID(actorID[i+1:]) {
		return TaskTypeActivity
	}
	return TaskTypeWorkflow
}

// siblingRendezvousKey returns the rendezvous actor ID used by the other
// daprd version for the same activity task, or "" when actorID is not an
// activity rendezvous key. Pre-upgrade daprds key the activity rendezvous on
// the durabletask execution key "<instanceID>/<taskID>"; current daprds use
// the activity actor ID "<instanceID>::<taskID>". Workflow rendezvous keys
// (the bare instance ID) are format-stable across versions and translate to
// "" unless the instance ID itself happens to end in "::<digits>", in which
// case the spurious forward parks on an unwatched actor and is harmless.
// Instance IDs cannot contain "/" (the scheduler rejects such job names), so
// the first form only ever matches genuine pre-upgrade activity keys.
func siblingRendezvousKey(actorID string) string {
	if i := strings.LastIndex(actorID, "/"); i > 0 && isTaskID(actorID[i+1:]) {
		return actorID[:i] + "::" + actorID[i+1:]
	}
	if i := strings.LastIndex(actorID, "::"); i > 0 && isTaskID(actorID[i+2:]) {
		return actorID[:i] + "/" + actorID[i+2:]
	}
	return ""
}

// isTaskID reports whether s is a base-10 integer as produced by task ID
// formatting.
func isTaskID(s string) bool {
	if len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// forwardSibling forwards a completion to the sibling-format rendezvous
// actor. It bridges rolling upgrades: a completion routed with one version's
// activity rendezvous key still reaches a waiter that rendezvouses under the
// other version's key, instead of waiting for the durable reminder retry.
// Best effort; on failure the retry path still converges.
func (e *executor) forwardSibling(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) {
	sibling := siblingRendezvousKey(e.actorID)
	if sibling == "" {
		return
	}

	router, err := e.actors.Router(ctx)
	if err != nil {
		log.Debugf("Executor actor '%s': unable to forward completion to sibling rendezvous '%s': %s", e.actorID, sibling, err)
		return
	}

	// Only activity keys have sibling forms, so the forward is always an
	// activity completion.
	freq := internalsv1pb.
		NewInternalInvokeRequest(MethodComplete).
		WithActor(e.actorType, sibling).
		WithData(req.GetMessage().GetData().GetValue()).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{
			MetadataForwarded: {"true"},
			MetadataTaskType:  {TaskTypeActivity},
		})

	if _, err = router.Call(ctx, freq); err != nil {
		log.Debugf("Executor actor '%s': failed to forward completion to sibling rendezvous '%s': %s", e.actorID, sibling, err)
	}
}

func isForwarded(req *internalsv1pb.InternalInvokeRequest) bool {
	v, ok := req.GetMetadata()[MetadataForwarded]
	return ok && len(v.GetValues()) > 0 && v.GetValues()[0] == "true"
}
