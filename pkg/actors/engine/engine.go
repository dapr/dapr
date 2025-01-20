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

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/events/queue"
)

type Interface interface {
	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallReminder(ctx context.Context, reminder *api.Reminder) error
	CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error
}

type Options struct {
	Namespace          string
	Table              table.Interface
	Placement          placement.Interface
	Resiliency         resiliency.Provider
	Reminders          reminders.Interface
	GRPC               *manager.Manager
	IdlerQueue         *queue.Processor[string, targets.Idlable]
	SchedulerReminders bool
}

type engine struct {
	namespace          string
	schedulerReminders bool

	table      table.Interface
	placement  placement.Interface
	resiliency resiliency.Provider
	reminders  reminders.Interface
	grpc       *manager.Manager

	idlerQueue *queue.Processor[string, targets.Idlable]

	lock  *fifo.Mutex
	clock clock.Clock
}

func New(opts Options) Interface {
	return &engine{
		namespace:          opts.Namespace,
		schedulerReminders: opts.SchedulerReminders,
		table:              opts.Table,
		placement:          opts.Placement,
		resiliency:         opts.Resiliency,
		grpc:               opts.GRPC,
		idlerQueue:         opts.IdlerQueue,
		reminders:          opts.Reminders,
		lock:               fifo.New(),
		clock:              clock.RealClock{},
	}
}

func (e *engine) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var res *internalv1pb.InternalInvokeResponse
	var err error
	if e.resiliency.PolicyDefined(req.GetActor().GetActorType(), resiliency.ActorPolicy{}) {
		res, err = e.callActor(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, e.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		res, err = policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
			return e.callActor(ctx, req)
		})
	}

	return res, err
}

func (e *engine) CallReminder(ctx context.Context, req *api.Reminder) error {
	var err error
	if e.resiliency.PolicyDefined(req.ActorType, resiliency.ActorPolicy{}) {
		err = e.callReminder(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[struct{}](ctx, e.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		_, err = policyRunner(func(ctx context.Context) (struct{}, error) {
			return struct{}{}, e.callReminder(ctx, req)
		})
	}

	return err
}

func (e *engine) callReminder(ctx context.Context, req *api.Reminder) error {
	ctx, cancel, err := e.placement.Lock(ctx)
	if err != nil {
		return backoff.Permanent(err)
	}
	defer cancel()

	lar, err := e.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.ActorType,
		ActorID:   req.ActorID,
	})
	if err != nil {
		return err
	}

	if !lar.Local {
		if req.IsRemote {
			return backoff.Permanent(errors.New("remote actor moved"))
		}

		return e.callRemoteActorReminder(ctx, lar, req)
	}

	target, _, err := e.table.GetOrCreate(req.ActorType, req.ActorID)
	if err != nil {
		return backoff.Permanent(err)
	}

	if req.IsTimer {
		err = target.InvokeTimer(ctx, req)
	} else {
		err = target.InvokeReminder(ctx, req)
	}

	return backoff.Permanent(err)
}

func (e *engine) callActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	ctx, cancel, err := e.placement.Lock(ctx)
	if err != nil {
		return nil, backoff.Permanent(err)
	}
	defer cancel()

	lar, err := e.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		return nil, err
	}

	if lar.Local {
		var res *internalv1pb.InternalInvokeResponse
		res, err = e.callLocalActor(ctx, req)
		if err != nil {
			if merr, ok := err.(messages.APIError); ok &&
				merr.Is(messages.ErrActorMaxStackDepthExceeded) {
				return res, backoff.Permanent(err)
			}
			return res, err
		}
		return res, nil
	}

	// If this is a dapr-dapr call and the actor didn't pass the local check
	// above, it means it has been moved in the meantime
	if _, ok := req.GetMetadata()["X-Dapr-Remote"]; ok {
		return nil, backoff.Permanent(errors.New("remote actor moved"))
	}

	res, err := e.callRemoteActor(ctx, lar, req)
	if err == nil {
		return res, nil
	}

	attempt := resiliency.GetAttempt(ctx)
	code := status.Code(err)
	if code == codes.Unavailable || code == codes.Internal {
		// Destroy the connection and force a re-connection on the next attempt
		return res, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", lar.Address, attempt-1, err)
	}

	return res, backoff.Permanent(err)
}

func (e *engine) callRemoteActor(ctx context.Context, lar *api.LookupActorResponse, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	conn, cancel, err := e.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, e.namespace)
	if err != nil {
		return nil, err
	}
	defer cancel(false)

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

func (e *engine) callRemoteActorReminder(ctx context.Context, lar *api.LookupActorResponse, reminder *api.Reminder) error {
	conn, cancel, err := e.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, e.namespace)
	if err != nil {
		return err
	}
	defer cancel(false)

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
	target, err := e.getOrCreateActor(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	if err != nil {
		return nil, err
	}

	return target.InvokeMethod(ctx, req)
}

func (e *engine) CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error {
	ctx, pcancel, err := e.placement.Lock(ctx)
	if err != nil {
		return err
	}

	lar, err := e.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		pcancel()
		return err
	}

	if !lar.Local {
		return e.callRemoteActorStream(ctx, pcancel, lar, req, stream)
	}

	return e.callLocalActorStream(ctx, pcancel, req, stream)
}

func (e *engine) callLocalActorStream(ctx context.Context,
	pcancel context.CancelFunc,
	req *internalv1pb.InternalInvokeRequest,
	stream chan<- *internalv1pb.InternalInvokeResponse,
) error {
	target, err := e.getOrCreateActor(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	pcancel()

	if err != nil {
		return err
	}

	return target.InvokeStream(ctx, req, stream)
}

func (e *engine) callRemoteActorStream(ctx context.Context,
	pcancel context.CancelFunc,
	lar *api.LookupActorResponse,
	req *internalv1pb.InternalInvokeRequest,
	stream chan<- *internalv1pb.InternalInvokeResponse,
) error {
	conn, cancel, err := e.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, e.namespace)
	if err != nil {
		pcancel()
		return err
	}
	defer cancel(false)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	rstream, err := client.CallActorStream(ctx, req)
	if err != nil {
		return err
	}

	pcancel()

	for {
		resp, err := rstream.Recv()
		if err != nil {
			return err
		}

		select {
		case stream <- resp:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
