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

// Package grpc implements the transport.Invoker interface against the
// app-initiated SubscribeActorEventsAlpha1 stream. Callbacks are pushed to
// the app over the stream managed by pkg/actors/callbackstream; responses
// are correlated back by request id.
package grpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/callbackstream"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"

	"google.golang.org/protobuf/types/known/anypb"
)

// errorResponseHeader is the HTTP-protocol marker that downstream code
// (notably pkg/actors/router/router.go) inspects to decide whether a
// response represents an application-level actor error. The streaming
// transport synthesizes it on responses when error=true so router-side
// code keeps working unchanged.
const errorResponseHeader = "X-Daprerrorresponseheader"

// Transport delivers actor callbacks over the SubscribeActorEventsAlpha1
// stream registered in callbackstream.Manager.
type Transport struct {
	stream     *callbackstream.Manager
	resiliency resiliency.Provider
	actorType  string
}

// New constructs a streaming Transport wired to the supplied stream manager.
func New(stream *callbackstream.Manager, resiliency resiliency.Provider, actorType string) *Transport {
	return &Transport{
		stream:     stream,
		resiliency: resiliency,
		actorType:  actorType,
	}
}

// Invoke delivers an actor method call through the stream. The returned
// InternalInvokeResponse is synthesized from the app's reply so callers
// that rely on HTTP-shaped signals (notably the X-Daprerrorresponseheader
// router check) keep working without changes.
func (t *Transport) Invoke(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	actorID := req.GetActor().GetActorId()
	msg := req.GetMessage()

	policyDef := t.resiliency.ActorPostLockPolicy(t.actorType, actorID)
	policyRunner := resiliency.NewRunner[*runtimev1pb.SubscribeActorEventsRequestInvokeResponseAlpha1](ctx, policyDef)

	resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
		incoming, sendErr := t.stream.Send(ctx, func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
			return &runtimev1pb.SubscribeActorEventsResponseAlpha1{
				ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_InvokeRequest{
					InvokeRequest: &runtimev1pb.SubscribeActorEventsResponseInvokeRequestAlpha1{
						Id:        id,
						ActorType: t.actorType,
						ActorId:   actorID,
						Method:    msg.GetMethod(),
						Data:      msg.GetData().GetValue(),
						Metadata:  flattenFirstValue(req.GetMetadata()),
					},
				},
			}
		})
		if sendErr != nil {
			return nil, sendErr
		}
		if failed := incoming.GetRequestFailed(); failed != nil {
			if codes.Code(failed.GetCode()) == codes.NotFound {
				return nil, backoff.Permanent(fmt.Errorf("actor method not found: %s", msg.GetMethod()))
			}
			return nil, fmt.Errorf("error from actor service: %s", failed.GetMessage())
		}
		r := incoming.GetInvokeResponse()
		if r == nil {
			return nil, errors.New("actor host returned an unexpected response type for invoke")
		}
		return r, nil
	})
	if err != nil {
		return nil, err
	}

	internalResp := buildInternalResponse(resp)
	if resp.GetError() {
		return internalResp, actorerrors.NewActorError(internalResp)
	}
	return internalResp, nil
}

// InvokeReminder delivers a reminder fire. Data is sent as
// google.protobuf.Any so the app receives the typed payload originally
// registered on the reminder.
func (t *Transport) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	policyDef := t.resiliency.ActorPostLockPolicy(t.actorType, reminder.ActorID)
	policyRunner := resiliency.NewRunner[*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1](ctx, policyDef)

	resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
		incoming, sendErr := t.stream.Send(ctx, func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
			return &runtimev1pb.SubscribeActorEventsResponseAlpha1{
				ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_ReminderRequest{
					ReminderRequest: &runtimev1pb.SubscribeActorEventsResponseReminderRequestAlpha1{
						Id:        id,
						ActorType: reminder.ActorType,
						ActorId:   reminder.ActorID,
						Name:      reminder.Name,
						DueTime:   reminder.DueTime,
						Period:    reminder.Period.String(),
						Data:      reminder.Data,
					},
				},
			}
		})
		return reminderResult(incoming, sendErr)
	})
	if err != nil {
		return err
	}
	if resp.GetCancel() {
		return actorerrors.ErrReminderCanceled
	}
	return nil
}

// InvokeTimer delivers a timer fire. Shape mirrors InvokeReminder with the
// extra callback method name carried through.
func (t *Transport) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	policyDef := t.resiliency.ActorPostLockPolicy(t.actorType, reminder.ActorID)
	policyRunner := resiliency.NewRunner[*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1](ctx, policyDef)

	resp, err := policyRunner(func(ctx context.Context) (*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
		incoming, sendErr := t.stream.Send(ctx, func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
			return &runtimev1pb.SubscribeActorEventsResponseAlpha1{
				ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_TimerRequest{
					TimerRequest: &runtimev1pb.SubscribeActorEventsResponseTimerRequestAlpha1{
						Id:        id,
						ActorType: reminder.ActorType,
						ActorId:   reminder.ActorID,
						Name:      reminder.Name,
						DueTime:   reminder.DueTime,
						Period:    reminder.Period.String(),
						Callback:  reminder.Callback,
						Data:      reminder.Data,
					},
				},
			}
		})
		return timerResult(incoming, sendErr)
	})
	if err != nil {
		return err
	}
	if resp.GetCancel() {
		return actorerrors.ErrReminderCanceled
	}
	return nil
}

// Deactivate issues a deactivate callback through the stream.
func (t *Transport) Deactivate(ctx context.Context, actorType, actorID string) error {
	_, err := t.stream.Send(ctx, func(id string) *runtimev1pb.SubscribeActorEventsResponseAlpha1 {
		return &runtimev1pb.SubscribeActorEventsResponseAlpha1{
			ResponseType: &runtimev1pb.SubscribeActorEventsResponseAlpha1_DeactivateRequest{
				DeactivateRequest: &runtimev1pb.SubscribeActorEventsResponseDeactivateRequestAlpha1{
					Id:        id,
					ActorType: actorType,
					ActorId:   actorID,
				},
			},
		}
	})
	return err
}

// reminderResult extracts an SubscribeActorEventsRequestReminderResponseAlpha1 from an incoming
// stream message. A RequestFailed oneof is mapped to a Go error.
func reminderResult(incoming *runtimev1pb.SubscribeActorEventsRequestAlpha1, err error) (*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
	if err != nil {
		return nil, err
	}
	if failed := incoming.GetRequestFailed(); failed != nil {
		return nil, fmt.Errorf("error from actor service: %s", failed.GetMessage())
	}
	r := incoming.GetReminderResponse()
	if r == nil {
		return nil, errors.New("actor host returned an unexpected response type for reminder")
	}
	return r, nil
}

// timerResult is reminderResult's twin for OnActorTimer responses. Kept
// separate so the oneof-variant getter is specific to timers and future
// divergence stays localized.
func timerResult(incoming *runtimev1pb.SubscribeActorEventsRequestAlpha1, err error) (*runtimev1pb.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
	if err != nil {
		return nil, err
	}
	if failed := incoming.GetRequestFailed(); failed != nil {
		return nil, fmt.Errorf("error from actor service: %s", failed.GetMessage())
	}
	r := incoming.GetTimerResponse()
	if r == nil {
		return nil, errors.New("actor host returned an unexpected response type for timer")
	}
	return r, nil
}

// flattenFirstValue reduces the multi-value internal metadata map to the
// single-value shape the SubscribeActorEventsResponseInvokeRequestAlpha1.metadata field carries. The
// reentrancy id is included here so the app receives it alongside any
// other headers, matching the HTTP header semantics.
func flattenFirstValue(md map[string]*internalv1pb.ListStringValue) map[string]string {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md))
	for k, v := range md {
		if v == nil || len(v.GetValues()) == 0 {
			continue
		}
		out[k] = v.GetValues()[0]
	}
	return out
}

// buildInternalResponse converts an SubscribeActorEventsRequestInvokeResponseAlpha1 into the internal
// proto shape the rest of the runtime expects. When the app signals an
// actor error we also set the X-Daprerrorresponseheader marker so
// cross-daprd paths that inspect headers (router.callRemoteActor) keep
// recognizing the error.
func buildInternalResponse(resp *runtimev1pb.SubscribeActorEventsRequestInvokeResponseAlpha1) *internalv1pb.InternalInvokeResponse {
	headers := expandSingleValue(resp.GetMetadata())
	if resp.GetError() {
		headers[errorResponseHeader] = &internalv1pb.ListStringValue{Values: []string{"true"}}
	}
	return &internalv1pb.InternalInvokeResponse{
		Status:  &internalv1pb.Status{Code: 200},
		Headers: headers,
		Message: &commonv1pb.InvokeResponse{
			ContentType: resp.GetMetadata()["content-type"],
			Data:        &anypb.Any{Value: resp.GetData()},
		},
	}
}

// expandSingleValue inflates the single-value metadata map used on the wire
// back into the multi-value internal representation.
func expandSingleValue(m map[string]string) map[string]*internalv1pb.ListStringValue {
	out := make(map[string]*internalv1pb.ListStringValue, len(m)+1)
	for k, v := range m {
		out[k] = &internalv1pb.ListStringValue{Values: []string{v}}
	}
	return out
}
