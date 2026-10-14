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
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/targets/app/lock"
	"github.com/dapr/dapr/pkg/actors/targets/app/transport"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.app")

type app struct {
	*factory

	actorID      string
	activationMD *internalv1pb.ListStringValue

	// idleAt is the time after which this actor is considered to be idle.
	// When the actor is locked, idleAt is updated by adding the idleTimeout to
	// the current time.
	idleAt atomic.Pointer[time.Time]

	lock  *lock.Lock
	clock clock.Clock

	closed atomic.Bool
}

func (a *app) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	ctx, cancel, err := a.lock.LockRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cancel()

	a.touchIdle()

	md := make(map[string]*internalv1pb.ListStringValue, len(req.GetMetadata())+1)
	for k, v := range req.GetMetadata() {
		switch {
		case strings.EqualFold(k, transport.ActivationIDHeader):
		case strings.EqualFold(k, "Connection"):
			if conn := withoutActivationID(v); conn != nil {
				md[k] = conn
			}
		default:
			md[k] = v
		}
	}
	md[transport.ActivationIDHeader] = a.activationMD
	return a.transport.Invoke(ctx, &internalv1pb.InternalInvokeRequest{
		Ver:      req.GetVer(),
		Actor:    req.GetActor(),
		Message:  req.GetMessage(),
		Metadata: md,
	})
}

func (a *app) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	// Build a minimal request for the reentrancy lock keyed on
	// "remind/<name>". Reminder names may contain '/' (e.g. workflow-style
	// keys), so the name is passed through verbatim here — path-traversal
	// sanitization happens in the HTTP transport where the method is
	// embedded in a URL path.
	lockReq := internalv1pb.NewInternalInvokeRequest("remind/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID)

	ctx, cancel, err := a.lock.LockRequest(ctx, lockReq)
	if err != nil {
		return err
	}
	defer cancel()

	a.touchIdle()
	a.setActivationID(lockReq)
	log.Debug("Executing reminder for actor " + reminder.Key())

	if err := a.transport.InvokeReminder(ctx, reminder, lockReq.GetMetadata()); err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing reminder for actor %s: %v", reminder.Key(), err)
		}
		return err
	}
	return nil
}

func (a *app) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	// See InvokeReminder for the path-sanitization split between the app
	// layer and the HTTP transport.
	lockReq := internalv1pb.NewInternalInvokeRequest("timer/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID)

	wctx, removed, wcancel := reminder.UntilRemoved(ctx)
	defer wcancel()

	ctx, cancel, err := a.lock.LockRequest(wctx, lockReq)
	if removed() {
		if err == nil {
			cancel()
		}
		log.Debugf("Dropping timer %s: timer was removed", reminder.Key())
		return actorerrors.ErrTimerFireStale
	}
	if err != nil {
		return err
	}
	defer cancel()

	a.touchIdle()
	a.setActivationID(lockReq)
	log.Debug("Executing timer for actor " + reminder.Key())

	if err := a.transport.InvokeTimer(ctx, reminder, lockReq.GetMetadata()); err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing timer for actor %s: %v", reminder.Key(), err)
		}
		return err
	}
	return nil
}

func (a *app) Deactivate(ctx context.Context) error {
	if !a.closed.CompareAndSwap(false, true) {
		return nil
	}

	a.lock.Close(ctx)
	a.table.Delete(a.actorID)

	if err := a.transport.Deactivate(context.Background(), a.actorType, a.actorID); err != nil {
		return err
	}

	a.idlerQueue.Dequeue(key.ConstructComposite(a.actorType, a.actorID))
	diag.DefaultMonitoring.ActorDeactivated(a.actorType)
	log.Debugf("Deactivated actor '%s'", a.Key())
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

func (a *app) setActivationID(req *internalv1pb.InternalInvokeRequest) {
	if req.GetMetadata() == nil {
		req.Metadata = make(map[string]*internalv1pb.ListStringValue, 1)
	}
	req.GetMetadata()[transport.ActivationIDHeader] = a.activationMD
}

// withoutActivationID drops the activation id from a Connection header so a
// caller can't strip it as a hop-by-hop header.
func withoutActivationID(conn *internalv1pb.ListStringValue) *internalv1pb.ListStringValue {
	var values []string
	for _, v := range conn.GetValues() {
		var tokens []string
		for token := range strings.SplitSeq(v, ",") {
			token = strings.TrimSpace(token)
			if token != "" && !strings.EqualFold(token, transport.ActivationIDHeader) {
				tokens = append(tokens, token)
			}
		}
		if len(tokens) > 0 {
			values = append(values, strings.Join(tokens, ", "))
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &internalv1pb.ListStringValue{Values: values}
}

// touchIdle bumps the actor's idle deadline and re-enqueues it in the idler
// queue so the deactivation timer restarts.
func (a *app) touchIdle() {
	a.idleAt.Store(new(a.clock.Now().Add(a.idleTimeout)))
	a.idlerQueue.Enqueue(a)
}
