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

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messages"
)

// SetActorsInitDone indicates that the actors runtime has been initialized, whether actors are available or not
func (a *Universal) SetActorsInitDone() {
	if a.actorsReady.CompareAndSwap(false, true) {
		close(a.actorsReadyCh)
	}
}

// WaitForActorsReady blocks until the actor runtime is set in the object (or until the context is canceled).
func (a *Universal) WaitForActorsReady(ctx context.Context) {
	// Quick check to avoid allocating a timer if the actors are ready
	if a.actorsReady.Load() {
		return
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()

	// In both cases, it's a no-op as we check for the actors runtime to be ready below
	select {
	case <-waitCtx.Done():
	case <-a.actorsReadyCh:
	}
}

// ActorReadinessCheck makes sure that the actor subsystem is ready.
func (a *Universal) ActorReadinessCheck(ctx context.Context) error {
	a.WaitForActorsReady(ctx)

	if a.Actors() == nil {
		a.logger.Debug(messages.ErrActorRuntimeNotFound)
		return messages.ErrActorRuntimeNotFound
	}

	return nil
}

func (a *Universal) RegisterActorTimer(ctx context.Context, in *runtimev1pb.RegisterActorTimerRequest) (*emptypb.Empty, error) {
	err := a.ActorReadinessCheck(ctx)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	req := &actors.CreateTimerRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
		DueTime:   in.GetDueTime(),
		Period:    in.GetPeriod(),
		TTL:       in.GetTtl(),
		Callback:  in.GetCallback(),
	}

	if in.GetData() != nil {
		j, err := json.Marshal(in.GetData())
		if err != nil {
			return &emptypb.Empty{}, err
		}
		req.Data = j
	}
	err = a.Actors().CreateTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *Universal) UnregisterActorTimer(ctx context.Context, in *runtimev1pb.UnregisterActorTimerRequest) (*emptypb.Empty, error) {
	err := a.ActorReadinessCheck(ctx)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteTimerRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
	}

	err = a.Actors().DeleteTimer(ctx, req)
	return &emptypb.Empty{}, err
}

func (a *Universal) RegisterActorReminder(ctx context.Context, in *runtimev1pb.RegisterActorReminderRequest) (*emptypb.Empty, error) {
	err := a.ActorReadinessCheck(ctx)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	req := &actors.CreateReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
		DueTime:   in.GetDueTime(),
		Period:    in.GetPeriod(),
		TTL:       in.GetTtl(),
	}

	if in.GetData() != nil {
		j, err := json.Marshal(in.GetData())
		if err != nil {
			return &emptypb.Empty{}, err
		}
		req.Data = j
	}
	err = a.Actors().CreateReminder(ctx, req)
	if err != nil && errors.Is(err, actors.ErrReminderOpActorNotHosted) {
		a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
		return nil, messages.ErrActorReminderOpActorNotHosted
	}
	return &emptypb.Empty{}, err
}

func (a *Universal) UnregisterActorReminder(ctx context.Context, in *runtimev1pb.UnregisterActorReminderRequest) (*emptypb.Empty, error) {
	err := a.ActorReadinessCheck(ctx)
	if err != nil {
		return &emptypb.Empty{}, err
	}

	req := &actors.DeleteReminderRequest{
		Name:      in.GetName(),
		ActorID:   in.GetActorId(),
		ActorType: in.GetActorType(),
	}

	err = a.Actors().DeleteReminder(ctx, req)
	if err != nil && errors.Is(err, actors.ErrReminderOpActorNotHosted) {
		a.logger.Debug(messages.ErrActorReminderOpActorNotHosted)
		return nil, messages.ErrActorReminderOpActorNotHosted
	}
	return &emptypb.Empty{}, err
}
