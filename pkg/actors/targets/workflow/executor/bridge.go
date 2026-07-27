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

	freq := internalsv1pb.
		NewInternalInvokeRequest(MethodComplete).
		WithActor(e.actorType, sibling).
		WithData(req.GetMessage().GetData().GetValue()).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{MetadataForwarded: {"true"}})

	if _, err = router.Call(ctx, freq); err != nil {
		log.Debugf("Executor actor '%s': failed to forward completion to sibling rendezvous '%s': %s", e.actorID, sibling, err)
	}
}

func isForwarded(req *internalsv1pb.InternalInvokeRequest) bool {
	v, ok := req.GetMetadata()[MetadataForwarded]
	return ok && len(v.GetValues()) > 0 && v.GetValues()[0] == "true"
}
