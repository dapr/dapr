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

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

	req := &api.CreateReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
		DueTime:   in.GetDueTime(),
		Period:    in.GetPeriod(),
		TTL:       in.GetTtl(),
		Data:      data,
	}

	err = r.Create(ctx, req)
	if err != nil {
		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
			return nil, messages.ErrActorReminderOpActorNotHosted
		}

		err = messages.ErrActorReminderCreate.WithFormat(err)
		a.logger.Debug(err)
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
