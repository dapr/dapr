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

// Package transport defines the Invoker interface that isolates HTTP and gRPC
// wire details from the rest of the actor target code. Implementations live in
// transport/http and transport/grpc.
package transport

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/api"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Header keys shared by both transports so the wire contract for app-side
// actor callback responses can't drift between HTTP and gRPC.
const (
	// ErrorResponseHeader is set by the app on a callback response to
	// signal an application-level actor error. The HTTP transport reads
	// it from the response headers as-is; the gRPC transport synthesizes
	// it on the InternalInvokeResponse when the app sets error=true so
	// cross-daprd paths (notably pkg/actors/router/router.go) can detect
	// the error from response headers alone.
	ErrorResponseHeader = "X-Daprerrorresponseheader"

	// ReminderCancelHeader is set by the app on a reminder or timer
	// response to ask Dapr to stop firing the recurring callback.
	ReminderCancelHeader = "X-Daprremindercancel"
)

// Invoker delivers actor callbacks to the user application.
//
// Error semantics:
//   - Invoke returns the raw InternalInvokeResponse so callers can forward
//     response headers verbatim. When the app signals an application-level
//     error, the returned error is an *actorerrors.ActorError and the
//     response carries the ErrorResponseHeader header (synthesized when
//     necessary so cross-daprd paths can detect the error from headers).
//   - InvokeReminder and InvokeTimer return actorerrors.ErrReminderCanceled
//     when the app asks to cancel the recurring callback.
//   - Method-not-found is wrapped in backoff.Permanent so resiliency treats
//     it as non-retryable.
type Invoker interface {
	Invoke(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	InvokeReminder(ctx context.Context, reminder *api.Reminder) error
	InvokeTimer(ctx context.Context, reminder *api.Reminder) error
	Deactivate(ctx context.Context, actorType, actorID string) error
}
