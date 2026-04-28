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
	"fmt"

	"google.golang.org/protobuf/proto"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// recursivePurgeWorkflowState handles the RecursivePurgeWorkflowStateMethod
// actor invocation. It is the entry point for "delegate this whole subtree's
// purge to me" — invoked either by a same-app caller cleaning a sub-tree of
// workflows or by a remote daprd's `Actors.PurgeWorkflowStateRecursive` for a
// cross-app sub-orchestration. The handler walks this workflow's children,
// recursively invokes itself on each child's owning actor (same-app local,
// or cross-app via the per-app workflow actor type), then cleans up its own
// state. Returns the total number of instances deleted, encoded as a
// `protos.PurgeInstancesResponse`.
//
// This mirrors the recursive-terminate model: each app handles its own
// subtree using local actor invocations, with a single cross-app hop at each
// app boundary.
func (o *orchestrator) recursivePurgeWorkflowState(ctx context.Context, meta map[string]*internalsv1pb.ListStringValue) ([]byte, error) {
	defer o.deactivate(o)

	force := metaFlagSet(meta, todo.MetadataPurgeForce)
	log.Debugf("Workflow actor '%s': recursive purge (force=%v)", o.actorID, force)

	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	if !force {
		if o.rstate.Stalled != nil {
			return nil, api.ErrStalled
		}
		if !runtimestate.IsCompleted(o.rstate) {
			return nil, api.ErrNotCompleted
		}
	}

	deleted := int32(0)

	for _, child := range collectChildren(state.History) {
		actorType := o.actorType
		if child.targetAppID != "" && child.targetAppID != o.appID {
			actorType = o.actorTypeBuilder.Workflow(child.targetAppID)
		}

		var count int32
		count, err = o.invokeRecursivePurge(ctx, actorType, child.instanceID, force)
		deleted += count
		if err != nil {
			return nil, fmt.Errorf("failed to purge child workflow %q: %w", child.instanceID, err)
		}
	}

	if err = o.cleanupWorkflowStateInternal(ctx, state, true); err != nil {
		return nil, err
	}
	deleted++

	resp, err := proto.Marshal(&protos.PurgeInstancesResponse{DeletedInstanceCount: deleted})
	if err != nil {
		return nil, fmt.Errorf("failed to encode recursive purge response: %w", err)
	}
	return resp, nil
}

// invokeRecursivePurge invokes RecursivePurgeWorkflowStateMethod on the actor
// at (actorType, instanceID) and decodes the count from its response.
func (o *orchestrator) invokeRecursivePurge(ctx context.Context, actorType, instanceID string, force bool) (int32, error) {
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.RecursivePurgeWorkflowStateMethod).
		WithActor(actorType, instanceID).
		WithContentType(invokev1.ProtobufContentType)

	if force {
		req = req.WithMetadata(map[string][]string{
			todo.MetadataPurgeForce: {"true"},
		})
	}

	resp, err := o.router.Call(ctx, req)
	if err != nil {
		return 0, err
	}

	var r protos.PurgeInstancesResponse
	if err := proto.Unmarshal(resp.GetMessage().GetData().GetValue(), &r); err != nil {
		return 0, fmt.Errorf("failed to decode recursive purge response from %s: %w", actorType, err)
	}
	return r.GetDeletedInstanceCount(), nil
}

type childRef struct {
	instanceID  string
	targetAppID string
}

// collectChildren scans history for ChildWorkflowInstanceCreated events,
// preserving each child's hosting app id (from the event's Router) so the
// recursive purge can dispatch cross-app correctly.
func collectChildren(events []*backend.HistoryEvent) []childRef {
	seen := make(map[string]struct{}, len(events))
	out := make([]childRef, 0, len(events))
	for _, e := range events {
		c := e.GetChildWorkflowInstanceCreated()
		if c == nil {
			continue
		}
		if _, dup := seen[c.GetInstanceId()]; dup {
			continue
		}
		seen[c.GetInstanceId()] = struct{}{}
		ref := childRef{instanceID: c.GetInstanceId()}
		if r := e.GetRouter(); r != nil {
			ref.targetAppID = r.GetTargetAppID()
		}
		out = append(out, ref)
	}
	return out
}
