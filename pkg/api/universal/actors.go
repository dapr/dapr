/*
Copyright 2023 The Dapr Authors
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

package universal

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/ptr"
)

func (a *Universal) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error) {
	timers, err := a.ActorTimers(ctx)
	if err != nil {
		return nil, err
	}

	var data *anypb.Any
	if b := in.GetData(); b != nil {
		b, err = json.Marshal(b)
		if err != nil {
			err = messages.ErrMalformedRequest.WithFormat(err)
			a.logger.Debug(err)
			return nil, err
		}

		data, err = anypb.New(wrapperspb.Bytes(b))
		if err != nil {
			err = messages.ErrMalformedRequest.WithFormat(err)
			a.logger.Debug(err)
			return nil, err
		}
	}

	req := &api.CreateTimerRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
		DueTime:   in.GetDueTime(),
		Period:    in.GetPeriod(),
		TTL:       in.GetTtl(),
		Callback:  in.GetCallback(),
		Data:      data,
	}

	err = timers.Create(ctx, req)
	if err != nil {
		err = messages.ErrActorTimerCreate.WithFormat(err)
		a.logger.Debug(err)
		return nil, err
	}
	return nil, nil
}

func (a *Universal) UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error) {
	timers, err := a.ActorTimers(ctx)
	if err != nil {
		return nil, err
	}

	req := &api.DeleteTimerRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
	}

	timers.Delete(ctx, req)
	return nil, nil
}

func (a *Universal) RegisterActorReminder(ctx context.Context, in *runtimev1pb.RegisterActorReminderRequest) (*emptypb.Empty, error) {
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	var data *anypb.Any
	if b := in.GetData(); b != nil {
		b, err = json.Marshal(b)
		if err != nil {
			err = messages.ErrMalformedRequest.WithFormat(err)
			a.logger.Debug(err)
			return nil, err
		}

		data, err = anypb.New(wrapperspb.Bytes(b))
		if err != nil {
			err = messages.ErrMalformedRequest.WithFormat(err)
			a.logger.Debug(err)
			return nil, err
		}
	}

	//nolint:protogetter
	req := &api.CreateReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
		DueTime:   in.GetDueTime(),
		Period:    in.GetPeriod(),
		TTL:       in.GetTtl(),
		Data:      data,
		Overwrite: in.Overwrite,
		//nolint:protogetter
		FailurePolicy: in.FailurePolicy,
	}

	err = r.Create(ctx, req)
	if err != nil {
		a.logger.Debug(err)

		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		status, ok := status.FromError(err)
		if ok && status.Code() == codes.AlreadyExists {
			return nil, messages.ErrActorReminderAlreadyExists.WithFormat(in.GetName())
		}

		err = messages.ErrActorReminderCreate.WithFormat(err)
		return nil, err
	}
	return nil, err
}

func (a *Universal) UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error) {
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	req := &api.DeleteReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
	}

	err = r.Delete(ctx, req)
	if err != nil {
		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		err = messages.ErrActorReminderDelete.WithFormat(err)
		a.logger.Debug(err)
		return nil, err
	}
	return nil, err
}

func (a *Universal) GetActorReminder(ctx context.Context, in *runtimev1pb.GetActorReminderRequest) (*runtimev1pb.GetActorReminderResponse, error) {
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := r.Get(ctx, &api.GetReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
	})
	if err != nil {
		a.logger.Debug(err)

		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		return nil, messages.ErrActorReminderGet.WithFormat(err)
	}

	if resp == nil {
		return nil, messages.ErrActorReminderNotFound.WithFormat(in.GetName())
	}

	var dueTime *string
	var period *string
	var ttl *string
	if resp.DueTime != "" {
		dueTime = ptr.Of(resp.DueTime)
	}
	if resp.Period.String() != "" {
		period = ptr.Of(resp.Period.String())
	}
	if !resp.ExpirationTime.IsZero() {
		ttl = ptr.Of(resp.ExpirationTime.Format(time.RFC3339))
	}

	return &runtimev1pb.GetActorReminderResponse{
		ActorType: resp.ActorType,
		ActorId:   resp.ActorID,
		DueTime:   dueTime,
		Period:    period,
		Ttl:       ttl,
		Data:      resp.Data,
	}, nil
}

func (a *Universal) UnregisterActorRemindersByType(ctx context.Context, in *runtimev1pb.UnregisterActorRemindersByTypeRequest) (*runtimev1pb.UnregisterActorRemindersByTypeResponse, error) {
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	req := &api.DeleteRemindersByActorIDRequest{
		ActorType:       in.GetActorType(),
		MatchIDAsPrefix: true,
	}
	if in.ActorId != nil {
		req.ActorID = in.GetActorId()
		req.MatchIDAsPrefix = false
	}

	err = r.DeleteByActorID(ctx, req)
	if err != nil {
		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		err = messages.ErrActorReminderDelete.WithFormat(err)
		a.logger.Debug(err)
		return nil, err
	}

	return new(runtimev1pb.UnregisterActorRemindersByTypeResponse), nil
}

func (a *Universal) ListActorReminders(ctx context.Context, req *runtimev1pb.ListActorRemindersRequest) (*runtimev1pb.ListActorRemindersResponse, error) {
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	//nolint:protogetter
	resp, err := r.List(ctx, &api.ListRemindersRequest{
		ActorType: req.GetActorType(),
		ActorID:   req.ActorId,
	})
	if err != nil {
		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		a.logger.Debug(err)
		return nil, err
	}

	reminders := make([]*runtimev1pb.NamedActorReminder, len(resp))

	for i, r := range resp {
		var dueTime *string
		var period *string
		if r.DueTime != "" {
			dueTime = &r.DueTime
		}
		if r.Period.String() != "" {
			period = ptr.Of(r.Period.String())
		}
		var expirationTime *string
		if !r.ExpirationTime.IsZero() {
			expirationTime = ptr.Of(r.ExpirationTime.Format(time.RFC3339Nano))
		}

		reminders[i] = &runtimev1pb.NamedActorReminder{
			Name: r.Name,
			Reminder: &runtimev1pb.ActorReminder{
				ActorType: r.ActorType,
				ActorId:   r.ActorID,
				DueTime:   dueTime,
				Period:    period,
				Ttl:       expirationTime,
				Data:      r.Data,
			},
		}
	}

	return &runtimev1pb.ListActorRemindersResponse{
		Reminders: reminders,
	}, nil
}
