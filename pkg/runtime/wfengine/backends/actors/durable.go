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

package actors

import (
	"context"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// completeDurably delivers a result with deliver and answers the worker only
// once the result is saved.
//
// The work item that took the result hands its Durable to the call through
// abe.durables: on this daprd when the result was delivered here, and as the
// owner's answer when it was forwarded to the daprd that owns the task. A
// result that no work item took is answered at once, as before: it was
// discarded as stale, fed a workflow that continues as new, or reached a
// daprd that does not wait for saves.
func (abe *Actors) completeDurably(ctx context.Context, key, token string, deliver func() error) error {
	receipt := abe.durables.Expect(key, token)
	err := abe.callWithBackoff(ctx, deliver)
	durable := receipt.Close()
	if err != nil {
		return ignoreUnknownTask(err)
	}
	if durable == nil {
		return nil
	}
	if err := waitDurable(ctx, durable); err != nil {
		return err
	}
	// Not a gRPC server context when called in-process.
	_ = grpc.SetHeader(ctx, metadata.Pairs(todo.DurableHeader, "true"))
	return nil
}

func waitDurable(ctx context.Context, durable *todo.Durable) error {
	// A save that settled as ctx ended is still reported as saved.
	select {
	case <-durable.Done():
	default:
		select {
		case <-durable.Done():
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		}
	}
	if err := durable.Err(); err != nil {
		return status.Errorf(codes.Aborted, "result was not saved: %s", err)
	}
	return nil
}

// wantsDurable reports whether the worker sent todo.DurableHeader with the
// value "true" on the call.
//
// Waiting is opt-in for activities: the wait lasts until the parent workflow
// takes the result, which can be behind a turn that waits for the worker. A
// worker that runs activities and workflows on one bounded pool of threads
// can then block every thread on answers that wait for each other.
func wantsDurable(ctx context.Context) bool {
	return slices.Contains(metadata.ValueFromIncomingContext(ctx, todo.DurableHeader), "true")
}
