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

package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/utils/clock"

	"github.com/cenkalti/backoff/v4"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
	"google.golang.org/grpc/status"
)

var log = logger.NewLogger("dapr.runtime.actor.engine")

type Interface interface {
	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallReminder(ctx context.Context, reminder *requestresponse.Reminder) error
}

type Options struct {
	Namespace   string
	HostAddress string
	Port        int
	Table       table.Interface
	Placement   placement.Interface
	Resiliency  resiliency.Provider
	GRPC        *manager.Manager
	IdlerQueue  *queue.Processor[string, targets.Idlable]
}

type engine struct {
	namespace   string
	hostAddress string
	port        int

	table      table.Interface
	placement  placement.Interface
	resiliency resiliency.Provider
	grpc       *manager.Manager

	idlerQueue *queue.Processor[string, targets.Idlable]

	lock    *fifo.Mutex
	closeCh chan struct{}
	clock   clock.Clock
}

func New(opts Options) Interface {
	return &engine{
		namespace:   opts.Namespace,
		hostAddress: opts.HostAddress,
		port:        opts.Port,
		table:       opts.Table,
		placement:   opts.Placement,
		resiliency:  opts.Resiliency,
		grpc:        opts.GRPC,
		idlerQueue:  opts.IdlerQueue,
		lock:        fifo.New(),
		closeCh:     make(chan struct{}),
	}
}

func (e *engine) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var res *internalv1pb.InternalInvokeResponse
	var err error
	if e.resiliency.PolicyDefined(req.GetActor().GetActorType(), resiliency.ActorPolicy{}) {
		res, err = e.callActor(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, e.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries))
		res, err = policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
			return e.callActor(ctx, req)
		})
	}

	if err != nil {
		if res != nil && actorerrors.Is(err) {
			return res, err
		}
		return nil, err
	}

	return res, nil
}

func (e *engine) CallReminder(ctx context.Context, req *requestresponse.Reminder) error {
	e.placement.Lock()
	defer e.placement.Unlock()

	target, ok := e.table.HostedTarget(req.ActorType, req.ActorID)
	if !ok {
		return e.callRemoteActorReminder(ctx, req)
	}

	var err error
	if req.IsTimer {
		err = target.InvokeTimer(ctx, req)
	} else {
		err = target.InvokeReminder(ctx, req)
	}

	// If the reminder was cancelled, delete it.
	// TODO: @joshvanl
	if errors.Is(err, actorerrors.ErrReminderCanceled) {
		//a.lock.Lock()
		//key := constructCompositeKey(reminder.ActorType, reminder.ActorID)
		//if act, ok := a.internalActors.Get(key); ok && act.Completed() {
		//	a.internalActors.Del(key)
		//	a.actorsTable.Delete(key)
		//}
		//a.lock.Unlock()
		//go func() {
		//	log.Debugf("Deleting reminder which was cancelled: %s", reminder.Key())
		//	reqCtx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		//	defer cancel()
		//	if derr := e.reminders.DeleteReminder(reqCtx, &requestresponse.DeleteReminderRequest{
		//		Name:      req.Name,
		//		ActorType: req.ActorType,
		//		ActorID:   req.ActorID,
		//	}); derr != nil {
		//		log.Errorf("Error deleting reminder %s: %s", req.Key(), derr)
		//	}
		//}()
	}

	return err
}

var foo = rand.Int()

func (e *engine) callActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	e.placement.Lock()
	defer e.placement.Unlock()

	actor := req.GetActor()
	actorType := actor.GetActorType()
	lar, err := e.placement.LookupActor(ctx, &requestresponse.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actor.GetActorId(),
	})
	if err != nil {
		return nil, err
	}

	if e.placement.IsActorLocal(lar.Address, e.hostAddress, e.port) {
		res, err := e.callLocalActor(ctx, req)
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		return res, nil
	}

	if _, ok := req.GetMetadata()["X-Dapr-Remote"]; ok {
		return nil, backoff.Permanent(errors.New("remote actor moved"))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := e.placement.SubscribeDisemination(ctx)
	var isDisseminating bool
	go func() {
		select {
		case <-ch:
			isDisseminating = true
			cancel()
		case <-ctx.Done():
		}
	}()

	res, err := e.callRemoteActor(ctx, lar, req)
	if err == nil {
		return res, nil
	}

	attempt := resiliency.GetAttempt(ctx)
	code := status.Code(err)
	if isDisseminating || code == codes.Unavailable || code == codes.Internal {
		// Destroy the connection and force a re-connection on the next attempt
		return res, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", lar.Address, attempt-1, err)
	}

	return res, backoff.Permanent(err)
}

func (e *engine) callRemoteActor(ctx context.Context, lar *requestresponse.LookupActorResponse, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	conn, cancel, err := e.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, e.namespace)
	if err != nil {
		return nil, err
	}
	defer cancel(true)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	res, err := client.CallActor(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(res.GetHeaders()["X-Daprerrorresponseheader"].GetValues()) > 0 {
		return res, actorerrors.NewActorError(res)
	}

	return res, nil
}

func (e *engine) callRemoteActorReminder(ctx context.Context, reminder *requestresponse.Reminder) error {
	lar, err := e.placement.LookupActor(ctx, &requestresponse.LookupActorRequest{
		ActorType: reminder.ActorType,
		ActorID:   reminder.ActorID,
	})
	if err != nil {
		return err
	}

	conn, cancel, err := e.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, e.namespace)
	if err != nil {
		return err
	}
	defer cancel(true)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	_, err = client.CallActorReminder(ctx, &internalv1pb.Reminder{
		ActorId:        reminder.ActorID,
		ActorType:      reminder.ActorType,
		Name:           reminder.Name,
		Data:           reminder.Data,
		Period:         reminder.Period.String(),
		DueTime:        reminder.DueTime,
		RegisteredTime: timestamppb.New(reminder.RegisteredTime),
		ExpirationTime: timestamppb.New(reminder.ExpirationTime),
		IsTimer:        reminder.IsTimer,
	})

	return err
}

func (e *engine) callLocalActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	target, err := e.getOrCreateActor(req.Actor.ActorType, req.Actor.ActorId)
	if err != nil {
		return nil, err
	}

	return target.InvokeMethod(ctx, req)
}

func (e *engine) getOrCreateActor(actorType, actorID string) (targets.Interface, error) {
	target, created, err := e.table.GetOrCreate(actorType, actorID)
	if err != nil {
		return nil, err
	}

	if created {
		// If the target is idlable, then add it to the queue.
		if idler, ok := target.(targets.Idlable); ok {
			e.idlerQueue.Enqueue(idler)
		}
	}

	return target, nil
}
