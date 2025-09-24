/*
Copyright 2024 The Dapr Authors
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

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/targets/app/lock"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
	"github.com/dapr/kit/strings"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.app")

type app struct {
	*factory

	actorID string

	// idleAt is the time after which this actor is considered to be idle.
	// When the actor is locked, idleAt is updated by adding the idleTimeout to
	// the current time.
	idleAt atomic.Pointer[time.Time]

	lock  *lock.Lock
	clock clock.Clock
}

func (a *app) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	ctx, cancel, err := a.lock.LockRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cancel()

	return a.doInvokeMethod(ctx, req)
}

func (a *app) doInvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	a.idleAt.Store(ptr.Of(a.clock.Now().Add(a.idleTimeout)))
	a.idlerQueue.Enqueue(a)

	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	// Replace method to actors method.
	msg := imReq.Message()
	originalMethod := msg.GetMethod()
	msg.Method = "actors/" + a.actorType + "/" + a.actorID + "/method/" + msg.GetMethod()

	// Reset the method so we can perform retries.
	defer func() {
		msg.Method = originalMethod
	}()

	// Per API contract, actor invocations over HTTP always use PUT as request method
	if msg.GetHttpExtension() == nil {
		req.WithHTTPExtension(http.MethodPut, "")
	} else {
		msg.HttpExtension.Verb = commonv1pb.HTTPExtension_PUT //nolint:nosnakecase
	}

	if a.appChannel == nil {
		return nil, fmt.Errorf("app channel for actor type %s is nil", a.actorType)
	}

	policyDef := a.resiliency.ActorPostLockPolicy(a.actorType, a.actorID)

	// If the request can be retried, we need to enable replaying
	if policyDef != nil && policyDef.HasRetries() {
		imReq.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	imRes, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return a.appChannel.InvokeMethod(ctx, imReq, "")
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

	// Get the protobuf
	res, err := imRes.ProtoWithData()
	if err != nil {
		return nil, fmt.Errorf("failed to read response data: %w", err)
	}

	// The .NET SDK indicates Actor failure via a header instead of a bad response
	if _, ok := res.GetHeaders()["X-Daprerrorresponseheader"]; ok {
		return res, actorerrors.NewActorError(res)
	}

	// Allow stopping a recurring reminder or timer
	if v := res.GetHeaders()["X-Daprremindercancel"]; v != nil && len(v.GetValues()) > 0 && strings.IsTruthy(v.GetValues()[0]) {
		return res, actorerrors.ErrReminderCanceled
	}

	return res, nil
}

func (a *app) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	ctx, cancel, err := a.lock.Lock(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	a.idleAt.Store(ptr.Of(a.clock.Now().Add(a.idleTimeout)))
	a.idlerQueue.Enqueue(a)

	invokeMethod := "remind/" + reminder.Name
	data, err := json.Marshal(&api.ReminderResponse{
		DueTime: reminder.DueTime,
		Period:  reminder.Period.String(),
		Data:    reminder.Data,
	})
	if err != nil {
		return err
	}
	log.Debug("Executing reminder for actor " + reminder.Key())

	req := internalv1pb.NewInternalInvokeRequest(invokeMethod).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)

	_, err = a.doInvokeMethod(ctx, req)
	if err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing reminder for actor %s: %v", reminder.Key(), err)
		}
		return err
	}

	return nil
}

func (a *app) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	ctx, cancel, err := a.lock.Lock(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	invokeMethod := "timer/" + reminder.Name
	data, err := json.Marshal(&api.TimerResponse{
		Callback: reminder.Callback,
		Data:     reminder.Data,
		DueTime:  reminder.DueTime,
		Period:   reminder.Period.String(),
	})
	if err != nil {
		return err
	}

	log.Debug("Executing timer for actor " + reminder.Key())

	req := internalv1pb.NewInternalInvokeRequest(invokeMethod).
		WithActor(reminder.ActorType, reminder.ActorID).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)

	_, err = a.doInvokeMethod(ctx, req)
	if err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing timer for actor %s: %v", reminder.Key(), err)
		}
		return err
	}

	return nil
}

func (a *app) Deactivate(ctx context.Context) error {
	defer a.table.Delete(a.actorID)

	a.lock.Close(ctx)

	req := invokev1.NewInvokeMethodRequest("actors/"+a.actorType+"/"+a.actorID).
		WithActor(a.actorType, a.actorID).
		WithHTTPExtension(http.MethodDelete, "").
		WithContentType(invokev1.ProtobufContentType)
	defer req.Close()

	resp, err := a.appChannel.InvokeMethod(context.Background(), req, "")
	if err != nil {
		diag.DefaultMonitoring.ActorDeactivationFailed(a.actorType, "invoke")
		return err
	}
	defer resp.Close()

	if resp.Status().GetCode() != http.StatusOK {
		diag.DefaultMonitoring.ActorDeactivationFailed(a.actorType, "status_code_"+strconv.FormatInt(int64(resp.Status().GetCode()), 10))
		body, _ := resp.RawDataFull()
		return fmt.Errorf("error from actor service: (%d) %s", resp.Status().GetCode(), string(body))
	}

	a.idlerQueue.Dequeue(key.ConstructComposite(a.actorType, a.actorID))
	diag.DefaultMonitoring.ActorDeactivated(a.actorType)
	log.Debugf("Deactivated actor '%s'", a.Key())
	appCache.Put(a)

	return nil
}

// Key returns the key for this unique actor.
func (a *app) Key() string {
	return a.actorType + api.DaprSeparator + a.actorID
}

// Type returns the actor type.
func (a *app) Type() string {
	return a.actorType
}

func (a *app) ID() string {
	return a.actorID
}

// ScheduledTime returns the time the actor becomes idle at.
// This is implemented to comply with the queueable interface.
func (a *app) ScheduledTime() time.Time {
	return *a.idleAt.Load()
}

func (a *app) InvokeStream(context.Context,
	*internalv1pb.InternalInvokeRequest,
	func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	return errors.New("not implemented")
}
