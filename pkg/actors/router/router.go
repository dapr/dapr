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

package router

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/table"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagutils "github.com/dapr/dapr/pkg/diagnostics/utils"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

type Interface interface {
	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallReminder(ctx context.Context, reminder *api.Reminder) error
	CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, fn func(*internalv1pb.InternalInvokeResponse) (bool, error)) error
}

type Options struct {
	Namespace          string
	Table              table.Interface
	Placement          placement.Interface
	Resiliency         resiliency.Provider
	Reminders          reminders.Interface
	GRPC               *manager.Manager
	SchedulerReminders bool
	MaxRequestBodySize int
}

type router struct {
	namespace          string
	schedulerReminders bool

	table      table.Interface
	placement  placement.Interface
	resiliency resiliency.Provider
	reminders  reminders.Interface
	grpc       *manager.Manager

	clock clock.Clock

	callOptions []grpc.CallOption
}

func New(opts Options) Interface {
	return &router{
		namespace:          opts.Namespace,
		schedulerReminders: opts.SchedulerReminders,
		table:              opts.Table,
		placement:          opts.Placement,
		resiliency:         opts.Resiliency,
		grpc:               opts.GRPC,
		reminders:          opts.Reminders,
		clock:              clock.RealClock{},
		callOptions: []grpc.CallOption{
			grpc.MaxCallRecvMsgSize(opts.MaxRequestBodySize),
			grpc.MaxCallSendMsgSize(opts.MaxRequestBodySize),
		},
	}
}

func (r *router) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	var res *internalv1pb.InternalInvokeResponse
	var err error

	if r.resiliency.PolicyDefined(req.GetActor().GetActorType(), resiliency.ActorPolicy{}) {
		res, err = r.callActor(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		res, err = policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
			return r.callActor(ctx, req)
		})
	}

	// Don't bubble perminant errors up to the caller to interfere with top level
	// retries.
	if _, ok := err.(*backoff.PermanentError); ok {
		err = errors.Unwrap(err)
	}

	return res, err
}

func (r *router) CallReminder(ctx context.Context, req *api.Reminder) error {
	if req.SkipLock {
		return r.callReminder(ctx, req)
	}

	if r.resiliency.PolicyDefined(req.ActorType, resiliency.ActorPolicy{}) {
		return r.callReminder(ctx, req)
	} else {
		policyRunner := resiliency.NewRunner[struct{}](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
		_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
			return struct{}{}, r.callReminder(ctx, req)
		})
		return err
	}
}

func (r *router) CallStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx, r.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries))
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		serr := r.callStream(ctx, req, stream)
		// Suppress EOF errors as this simply means the stream is closing.
		if errors.Is(serr, io.EOF) {
			return struct{}{}, nil
		}
		return struct{}{}, serr
	})

	return err
}

func (r *router) callReminder(ctx context.Context, req *api.Reminder) error {
	if !req.SkipLock {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = r.placement.Lock(ctx)
		if err != nil {
			return backoff.Permanent(err)
		}
		defer cancel()
	}

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
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

		err = r.callRemoteActorReminder(ctx, lar, req)
		status, ok := status.FromError(err)
		if ok && status.Code() == codes.Unavailable {
			return backoff.Permanent(err)
		}
		return err
	}

	for {
		target, err := r.table.GetOrCreate(req.ActorType, req.ActorID)
		if err != nil {
			return backoff.Permanent(err)
		}

		if req.IsTimer {
			err = target.InvokeTimer(ctx, req)
		} else {
			err = target.InvokeReminder(ctx, req)
		}

		if targetserrors.IsClosed(err) {
			continue
		}

		return backoff.Permanent(err)
	}
}

func (r *router) callActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	// If we are in a reentrancy which is local, skip the placement lock.
	_, isDaprRemote := req.GetMetadata()["X-Dapr-Remote"]
	_, isAPICall := req.GetMetadata()["Dapr-API-Call"]

	if isAPICall || isDaprRemote {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = r.placement.Lock(ctx)
		if err != nil {
			return nil, backoff.Permanent(err)
		}
		defer cancel()
	}

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to lookup actor: %w", err)
	}

	if lar.Local {
		for {
			var resp *internalv1pb.InternalInvokeResponse
			resp, err = r.callLocalActor(ctx, req)
			if err != nil {
				if targetserrors.IsClosed(err) {
					continue
				}
				return resp, backoff.Permanent(err)
			}
			return resp, nil
		}
	}

	// If this is a dapr-dapr call and the actor didn't pass the local check
	// above, it means it has been moved in the meantime
	if isDaprRemote {
		return nil, backoff.Permanent(errors.New("remote actor moved"))
	}

	res, err := r.callRemoteActor(ctx, lar, req)
	if err == nil {
		return res, nil
	}

	attempt := resiliency.GetAttempt(ctx)
	code := status.Code(err)
	if code == codes.Unavailable {
		// Destroy the connection and force a re-connection on the next attempt
		return res, fmt.Errorf("failed to invoke target %s after %d retries. Error: %w", lar.Address, attempt-1, err)
	}

	return res, backoff.Permanent(err)
}

func (r *router) callRemoteActor(ctx context.Context, lar *api.LookupActorResponse, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
	if err != nil {
		return nil, err
	}
	defer cancel(false)

	span := diagutils.SpanFromContext(ctx)
	ctx = diag.SpanContextToGRPCMetadata(ctx, span.SpanContext())
	client := internalv1pb.NewServiceInvocationClient(conn)

	res, err := client.CallActor(ctx, req, r.callOptions...)
	if err != nil {
		return nil, err
	}

	if len(res.GetHeaders()["X-Daprerrorresponseheader"].GetValues()) > 0 {
		return res, actorerrors.NewActorError(res)
	}

	return res, nil
}

func (r *router) callRemoteActorReminder(ctx context.Context, lar *api.LookupActorResponse, reminder *api.Reminder) error {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
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
		SkipLock:       reminder.SkipLock,
	})

	return err
}

func (r *router) callLocalActor(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	target, err := r.table.GetOrCreate(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	if err != nil {
		return nil, err
	}

	return target.InvokeMethod(ctx, req)
}

func (r *router) callStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	ctx, pcancel, err := r.placement.Lock(ctx)
	if err != nil {
		return backoff.Permanent(err)
	}
	defer pcancel()

	lar, err := r.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: req.GetActor().GetActorType(),
		ActorID:   req.GetActor().GetActorId(),
	})
	if err != nil {
		return err
	}

	if !lar.Local {
		// If this is a dapr-dapr call and the actor didn't pass the local check
		// above, it means it has been moved in the meantime
		if _, ok := req.GetMetadata()["X-Dapr-Remote"]; ok {
			return backoff.Permanent(errors.New("remote actor moved"))
		}

		return r.callRemoteActorStream(ctx, lar, req, stream)
	}

	return r.callLocalActorStream(ctx, req, stream)
}

func (r *router) callLocalActorStream(ctx context.Context,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	target, err := r.table.GetOrCreate(req.GetActor().GetActorType(), req.GetActor().GetActorId())
	if err != nil {
		return err
	}

	return target.InvokeStream(ctx, req, stream)
}

func (r *router) callRemoteActorStream(ctx context.Context,
	lar *api.LookupActorResponse,
	req *internalv1pb.InternalInvokeRequest,
	stream func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	conn, cancel, err := r.grpc.GetGRPCConnection(ctx, lar.Address, lar.AppID, r.namespace)
	if err != nil {
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

	for {
		resp, err := rstream.Recv()
		if err != nil {
			return err
		}

		if ok, err := stream(resp); err != nil || ok {
			return err
		}
	}
}
