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

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/reminders"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/messaging/method"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// RejectInternalActorType returns an error when actorType is a Dapr-reserved
// internal actor type (workflow, activity, executor, retentioner). The runtime
// owns these actors' lifecycle; direct user access via the generic actor APIs
// would corrupt workflow state or bypass per-operation policy.
func (a *Universal) RejectInternalActorType(actorType string) error {
	if !workflowacl.IsInternalActorType(actorType) {
		return nil
	}
	return apierrors.ActorTypeReserved(actorType)
}

func (a *Universal) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error) {
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
	timers, err := a.ActorTimers(ctx)
	if err != nil {
		return nil, err
	}

	var data *anypb.Any
	if b := in.GetData(); b != nil {
		b, err = json.Marshal(b)
		if err != nil {
			err = apierrors.ActorMalformedRequest(err)
			a.logger.Debug(err)
			return nil, err
		}

		data, err = anypb.New(wrapperspb.Bytes(b))
		if err != nil {
			err = apierrors.ActorMalformedRequest(err)
			a.logger.Debug(err)
			return nil, err
		}
	}

	if vErr := method.ValidateName(in.GetName()); vErr != nil {
		vErr = apierrors.ActorBadRequest(vErr)
		a.logger.Debug(vErr)
		return nil, vErr
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
		err = apierrors.ActorTimerCreate(err)
		a.logger.Debug(err)
		return nil, err
	}
	return nil, nil
}

func (a *Universal) UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error) {
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
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
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	var data *anypb.Any
	if b := in.GetData(); b != nil {
		b, err = json.Marshal(b)
		if err != nil {
			err = apierrors.ActorMalformedRequest(err)
			a.logger.Debug(err)
			return nil, err
		}

		data, err = anypb.New(wrapperspb.Bytes(b))
		if err != nil {
			err = apierrors.ActorMalformedRequest(err)
			a.logger.Debug(err)
			return nil, err
		}
	}

	if in.GetName() == "" {
		err = messages.ErrBadRequest.WithFormat("reminder name cannot be empty")
		a.logger.Debug(err)
		return nil, err
	}

	if vErr := method.ValidateName(in.GetName()); vErr != nil {
		vErr = apierrors.ActorBadRequest(vErr)
		a.logger.Debug(vErr)
		return nil, vErr
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
			return nil, apierrors.ActorReminderNonHosted()
		}

		status, ok := status.FromError(err)
		if ok && status.Code() == codes.AlreadyExists {
			return nil, apierrors.ActorReminderAlreadyExists(in.GetName())
		}

		err = apierrors.ActorReminderCreate(err)
		return nil, err
	}
	return nil, err
}

func (a *Universal) UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error) {
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
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
			nonHostedErr := apierrors.ActorReminderNonHosted()
			a.logger.Debug(nonHostedErr)
			return nil, nonHostedErr
		}

		err = apierrors.ActorReminderDelete(err)
		a.logger.Debug(err)
		return nil, err
	}
	return nil, err
}

func (a *Universal) GetActorReminder(ctx context.Context, in *runtimev1pb.GetActorReminderRequest) (*runtimev1pb.GetActorReminderResponse, error) {
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
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
			return nil, apierrors.ActorReminderNonHosted()
		}

		return nil, apierrors.ActorReminderGet(err)
	}

	if resp == nil {
		return nil, apierrors.ActorReminderNotFound(in.GetName())
	}

	var dueTime *string
	var period *string
	var ttl *string
	if resp.DueTime != "" {
		dueTime = new(resp.DueTime)
	}
	if resp.Period.String() != "" {
		period = new(resp.Period.String())
	}
	if !resp.ExpirationTime.IsZero() {
		ttl = new(resp.ExpirationTime.Format(time.RFC3339Nano))
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
	if err := a.RejectInternalActorType(in.GetActorType()); err != nil {
		return nil, err
	}
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
			nonHostedErr := apierrors.ActorReminderNonHosted()
			a.logger.Debug(nonHostedErr)
			return nil, nonHostedErr
		}

		err = apierrors.ActorReminderDelete(err)
		a.logger.Debug(err)
		return nil, err
	}

	return new(runtimev1pb.UnregisterActorRemindersByTypeResponse), nil
}

func (a *Universal) ListActorReminders(ctx context.Context, req *runtimev1pb.ListActorRemindersRequest) (*runtimev1pb.ListActorRemindersResponse, error) {
	if err := a.RejectInternalActorType(req.GetActorType()); err != nil {
		return nil, err
	}
	r, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := r.List(ctx, &api.ListRemindersRequest{
		ActorType: req.GetActorType(),
		ActorID:   req.ActorId,
	})
	if err != nil {
		if errors.Is(err, reminders.ErrReminderOpActorNotHosted) {
			nonHostedErr := apierrors.ActorReminderNonHosted()
			a.logger.Debug(nonHostedErr)
			return nil, nonHostedErr
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
			period = new(r.Period.String())
		}
		var expirationTime *string
		if !r.ExpirationTime.IsZero() {
			expirationTime = new(r.ExpirationTime.Format(time.RFC3339Nano))
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
