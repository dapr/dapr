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

// Package http implements the transport.Invoker interface against the legacy
// HTTP actor callback protocol. The wire shape is the original Dapr actor
// protocol: PUT /actors/{type}/{id}/method/{method}, POST/DELETE /actors/...,
// with X-Daprerrorresponseheader and X-Daprremindercancel response headers
// used to signal actor errors and reminder cancellation.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/channel"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/strings"
)

// Transport delivers actor callbacks over the HTTP callback protocol.
type Transport struct {
	appChannel channel.AppChannel
	resiliency resiliency.Provider
	actorType  string
}

// New constructs an HTTP Transport for the given actor type.
func New(appChannel channel.AppChannel, resiliency resiliency.Provider, actorType string) *Transport {
	return &Transport{
		appChannel: appChannel,
		resiliency: resiliency,
		actorType:  actorType,
	}
}

// Invoke runs req against the app over HTTP. Response headers and status code
// are preserved in the returned InternalInvokeResponse so upstream callers
// (router, cross-daprd service invocation) keep seeing the same signals.
func (t *Transport) Invoke(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	actorID := req.GetActor().GetActorId()

	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	// Rewrite the method to the actor method path and force PUT per the HTTP
	// actor protocol contract.
	msg := imReq.Message()
	originalMethod := msg.GetMethod()
	msg.Method = "actors/" + t.actorType + "/" + actorID + "/method/" + msg.GetMethod()
	defer func() {
		msg.Method = originalMethod
	}()

	if msg.GetHttpExtension() == nil {
		req.WithHTTPExtension(http.MethodPut, "")
	} else {
		msg.HttpExtension.Verb = commonv1pb.HTTPExtension_PUT //nolint:nosnakecase
	}

	if t.appChannel == nil {
		return nil, fmt.Errorf("app channel for actor type %s is nil", t.actorType)
	}

	policyDef := t.resiliency.ActorPostLockPolicy(t.actorType, actorID)
	if policyDef != nil && policyDef.HasRetries() {
		imReq.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	imRes, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return t.appChannel.InvokeMethod(ctx, imReq, "")
	})
	if err != nil {
		return nil, err
	}
	if imRes == nil {
		return nil, errors.New("error from actor service: response object is nil")
	}
	defer imRes.Close()

	if imRes.Status().GetCode() == http.StatusNotFound {
		return nil, backoff.Permanent(fmt.Errorf("actor method not found: %s", msg.GetMethod()))
	}
	if imRes.Status().GetCode() != http.StatusOK {
		respData, _ := imRes.RawDataFull()
		return nil, fmt.Errorf("error from actor service: (%d) %s", imRes.Status().GetCode(), string(respData))
	}

	res, err := imRes.ProtoWithData()
	if err != nil {
		return nil, fmt.Errorf("failed to read response data: %w", err)
	}

	// .NET SDK signals actor failure through a response header instead of a
	// non-2xx status code.
	if _, ok := res.GetHeaders()["X-Daprerrorresponseheader"]; ok {
		return res, actorerrors.NewActorError(res)
	}

	// Shared code path with reminders/timers means invoke responses may also
	// carry the reminder-cancel header. Only recurring callbacks act on it;
	// invoke callers simply surface the error.
	if v := res.GetHeaders()["X-Daprremindercancel"]; v != nil && len(v.GetValues()) > 0 && strings.IsTruthy(v.GetValues()[0]) {
		return res, actorerrors.ErrReminderCanceled
	}

	return res, nil
}

// InvokeReminder delivers a reminder fire. The app-facing payload is the JSON
// representation of api.ReminderResponse (Data is handled specially so SDKs
// receive it as a base64-encoded JSON blob rather than an Any wrapper).
func (t *Transport) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	data, err := json.Marshal(&api.ReminderResponse{
		DueTime: reminder.DueTime,
		Period:  reminder.Period.String(),
		Data:    reminder.Data,
	})
	if err != nil {
		return err
	}

	req := internalv1pb.NewInternalInvokeRequest("remind/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)

	_, err = t.Invoke(ctx, req)
	return err
}

// InvokeTimer delivers a timer fire. Shape mirrors InvokeReminder with the
// extra callback field.
func (t *Transport) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	data, err := json.Marshal(&api.TimerResponse{
		Callback: reminder.Callback,
		Data:     reminder.Data,
		DueTime:  reminder.DueTime,
		Period:   reminder.Period.String(),
	})
	if err != nil {
		return err
	}

	req := internalv1pb.NewInternalInvokeRequest("timer/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)

	_, err = t.Invoke(ctx, req)
	return err
}

// Deactivate issues a DELETE /actors/{type}/{id} to signal the app that the
// actor instance is being rebalanced or has gone idle.
func (t *Transport) Deactivate(ctx context.Context, actorType, actorID string) error {
	if t.appChannel == nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return fmt.Errorf("app channel for actor type %s is nil", actorType)
	}

	req := invokev1.NewInvokeMethodRequest("actors/"+actorType+"/"+actorID).
		WithActor(actorType, actorID).
		WithHTTPExtension(http.MethodDelete, "").
		WithContentType(invokev1.ProtobufContentType)
	defer req.Close()

	resp, err := t.appChannel.InvokeMethod(ctx, req, "")
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "invoke")
		return err
	}
	defer resp.Close()

	if resp.Status().GetCode() != http.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(actorType, "status_code_"+strconv.FormatInt(int64(resp.Status().GetCode()), 10))
		body, _ := resp.RawDataFull()
		return fmt.Errorf("error from actor service: (%d) %s", resp.Status().GetCode(), string(body))
	}

	return nil
}
