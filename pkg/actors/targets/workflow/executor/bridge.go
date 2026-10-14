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
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"

	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// MetadataForwarded marks a Complete call that was already forwarded from a
// sibling-format rendezvous actor, so it is never forwarded again.
const MetadataForwarded = "forwarded"

// forwardedValue is the MetadataForwarded value of a forwarded request, and
// of the header a parked forwarded completion keeps.
const forwardedValue = "true"

// MetadataHandoffs counts how many times a parked completion or cancellation
// moved to a fresh executor actor because the actor that held it retired. A
// request that carries it also carries the forwarded marker.
const MetadataHandoffs = "handoffs"

// maxHandoffs is the handoff budget of a parked result. During worker churn,
// several placement rounds can move a key before its watcher attaches. The
// budget still stops a result that nothing consumes from following every
// retirement.
const maxHandoffs = 3

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
	if _, _, ok := legacyActivityKey(actorID); ok {
		return TaskTypeActivity
	}
	return TaskTypeWorkflow
}

// siblingRendezvousKey returns the rendezvous actor ID used by the other
// daprd version for the same activity task, or "" when actorID is not an
// activity rendezvous key. Pre-upgrade daprds key the activity rendezvous on
// the durabletask execution key "<instanceID>/<taskID>"; current daprds use
// the activity actor ID "<instanceID>::<taskID>::<generation>". Workflow
// rendezvous keys (the bare instance ID) are format-stable across versions
// and translate to "" unless the instance ID itself happens to end in
// "::<digits>::<digits>", in which case the spurious forward parks on an
// unwatched actor and is harmless. Instance IDs cannot contain "/" (the
// scheduler rejects such job names), so the first form only ever matches
// genuine pre-upgrade activity keys.
func siblingRendezvousKey(actorID string) string {
	if iid, taskID, ok := legacyActivityKey(actorID); ok {
		return common.ActivityActorID(iid, taskID)
	}
	i := strings.LastIndex(actorID, common.ActivityIDSeparator)
	if i <= 0 || !isTaskID(actorID[i+2:]) {
		return ""
	}
	j := strings.LastIndex(actorID[:i], common.ActivityIDSeparator)
	if j <= 0 || !isTaskID(actorID[j+2:i]) {
		return ""
	}
	return actorID[:j] + "/" + actorID[j+2:i]
}

// legacyActivityKey reports whether actorID is a pre-upgrade activity
// rendezvous key "<instanceID>/<taskID>" and returns its parts. The shape is
// unambiguous: the scheduler rejects job names containing "/", so no
// workflow instance ID (and hence no current-format rendezvous key) can
// match it.
func legacyActivityKey(actorID string) (string, int32, bool) {
	if i := strings.LastIndex(actorID, "/"); i > 0 {
		if taskID, err := strconv.ParseInt(actorID[i+1:], 10, 32); err == nil {
			return actorID[:i], int32(taskID), true
		}
	}
	return "", 0, false
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

// forwardTimeout bounds a sibling forward. The forward is best effort (the
// durable reminder retry converges without it), so it must never hold
// resources for long.
const forwardTimeout = 10 * time.Second

// forwardSibling forwards a completion to the sibling-format rendezvous
// actor. It bridges rolling upgrades: a completion routed with one version's
// activity rendezvous key still reaches a waiter that rendezvouses under the
// other version's key, instead of waiting for the durable reminder retry.
// Best effort; on failure the retry path still converges.
//
// The forward runs in its own goroutine with a bounded, detached context and
// is deliberately not tracked by the actor's wait group: a slow cross-node
// call must not delay the completion reply, nor the actor's deactivation
// (Deactivate waits on the wait group, and the deactivation queue is drained
// serially). The goroutine only touches the actor's immutable identity
// fields, so it is safe past deactivation.
func (e *executor) forwardSibling(ctx context.Context, data []byte) {
	sibling := siblingRendezvousKey(e.actorID)
	if sibling == "" {
		return
	}

	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), forwardTimeout)
	go func() {
		defer cancel()
		e.callSibling(fctx, sibling, data)
	}()
}

func (e *executor) callSibling(ctx context.Context, sibling string, data []byte) {
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
		WithData(data).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{
			MetadataForwarded: {forwardedValue},
			MetadataTaskType:  {TaskTypeActivity},
		})

	if _, err = router.Call(ctx, freq); err != nil {
		log.Debugf("Executor actor '%s': failed to forward completion to sibling rendezvous '%s': %s", e.actorID, sibling, err)
	}
}

// parkedCancel is a cancellation recorded on an actor that nothing served
// yet: its task type ("" when there is none) and its handoff count.
type parkedCancel struct {
	taskType string
	handoffs int
}

// handOff sends what a retiring actor still held to the actor that now owns
// its key: each parked completion, then the recorded cancellation. After a
// rebalance the owner is another host. After an idle deactivation it is this
// host, where the call creates a fresh actor, which is where the next watch
// stream attaches. Each request carries the forwarded marker, so the receiver
// does not forward it to the sibling-format key, and the next handoff count,
// so a result that nothing consumes stops after maxHandoffs moves. A copy of
// a completion that the pending map already delivered, and a sibling copy,
// carry the marker without a count and never move. Best effort, in a
// background goroutine like forwardSibling, with its own forwardTimeout per
// call.
func (e *executor) handOff(ctx context.Context, parked []*internalsv1pb.InternalInvokeResponse, canc parkedCancel) {
	type handoff struct {
		req  *internalsv1pb.InternalInvokeRequest
		what string
	}

	var calls []handoff
	for _, d := range parked {
		n, ok := nextHandoff(d.GetHeaders())
		if !ok {
			continue
		}
		md := map[string][]string{
			MetadataForwarded: {forwardedValue},
			MetadataHandoffs:  {strconv.Itoa(n)},
		}
		if taskType := parkedTaskType(d); taskType != "" {
			md[MetadataTaskType] = []string{taskType}
		}
		calls = append(calls, handoff{
			what: "completion",
			req: internalsv1pb.
				NewInternalInvokeRequest(MethodComplete).
				WithActor(e.actorType, e.actorID).
				WithData(d.GetMessage().GetData().GetValue()).
				WithContentType(invokev1.ProtobufContentType).
				WithMetadata(md),
		})
	}
	if canc.taskType != "" && canc.handoffs < maxHandoffs {
		calls = append(calls, handoff{
			what: "cancellation",
			req: internalsv1pb.
				NewInternalInvokeRequest(MethodCancel).
				WithActor(e.actorType, e.actorID).
				WithContentType(invokev1.ProtobufContentType).
				WithMetadata(map[string][]string{
					MetadataForwarded: {forwardedValue},
					MetadataHandoffs:  {strconv.Itoa(canc.handoffs + 1)},
					MetadataTaskType:  {canc.taskType},
				}),
		})
	}
	if len(calls) == 0 {
		return
	}

	log.Debugf("Executor actor '%s': handing off %d parked result(s) to the owner of its key", e.actorID, len(calls))
	hctx := context.WithoutCancel(ctx)
	go func() {
		// One after another: they go to the same key, and the receiver
		// parks one completion at a time.
		for _, c := range calls {
			e.handOffOne(hctx, c.req, c.what)
		}
	}()
}

// handOffOne makes one handoff call within its own forwardTimeout. HaltAll
// runs after the type left this host's table, but placement resolves the key
// here until the type change is disseminated, which happens only after every
// HaltAll returns. The call then fails with ErrCreatingActor, so it is retried
// until the key resolves elsewhere.
func (e *executor) handOffOne(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, what string) {
	hctx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()

	err := backoff.Retry(func() error {
		router, err := e.actors.Router(hctx)
		if err != nil {
			return backoff.Permanent(err)
		}
		if _, err = router.Call(hctx, req); err != nil {
			if errors.Is(err, actorerrors.ErrCreatingActor) {
				return err
			}
			return backoff.Permanent(err)
		}
		return nil
	}, backoff.WithContext(backoff.NewConstantBackOff(handOffRetryInterval), hctx))
	if err != nil {
		log.Debugf("Executor actor '%s': failed to hand off a parked %s: %s", e.actorID, what, err)
		return
	}
	log.Debugf("Executor actor '%s': handed off a parked %s to the owner of its key", e.actorID, what)
}

// handOffRetryInterval is the pause between handoff attempts while the key
// still resolves to a host that no longer registers the type.
const handOffRetryInterval = 100 * time.Millisecond

// nextHandoff returns the handoff count for the next move of a parked result
// with the given headers, and whether it may move at all.
func nextHandoff(headers map[string]*internalsv1pb.ListStringValue) (int, bool) {
	if n, ok := handoffsIn(headers); ok {
		return n + 1, n < maxHandoffs
	}
	if forwardedIn(headers) {
		return 0, false
	}
	return 1, true
}

// handoffsIn reads the handoff count of a request's metadata or a parked
// result's headers.
func handoffsIn(m map[string]*internalsv1pb.ListStringValue) (int, bool) {
	v, ok := m[MetadataHandoffs]
	if !ok || len(v.GetValues()) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(v.GetValues()[0])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// forwardedIn reports whether a request's metadata or a parked result's
// headers carry the forwarded marker.
func forwardedIn(m map[string]*internalsv1pb.ListStringValue) bool {
	v, ok := m[MetadataForwarded]
	return ok && len(v.GetValues()) > 0 && v.GetValues()[0] == forwardedValue
}

func isForwarded(req *internalsv1pb.InternalInvokeRequest) bool {
	return forwardedIn(req.GetMetadata())
}
