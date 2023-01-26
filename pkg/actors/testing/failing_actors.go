/*
Copyright 2021 The Dapr Authors
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

package testing

import (
	"context"
	"errors"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	daprt "github.com/dapr/dapr/pkg/testing"
)

type FailingActors struct {
	Failure daprt.Failure
}

func (_m *FailingActors) RegisterInternalActor(ctx context.Context, actorType string, actor actors.InternalActor) error {
	return nil
}

func (f *FailingActors) Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	proto, err := req.ProtoWithData()
	if err != nil {
		return nil, err
	}
	if proto == nil || proto.Actor == nil {
		return nil, errors.New("proto.Actor is nil")
	}
	if err := f.Failure.PerformFailure(proto.Actor.ActorId); err != nil {
		return nil, err
	}
	var data []byte
	if proto.Message != nil && proto.Message.Data != nil {
		data = proto.Message.Data.Value
	}
	resp := invokev1.NewInvokeMethodResponse(200, "Success", nil).
		WithRawDataBytes(data)
	return resp, nil
}

func (f *FailingActors) Init() error {
	return nil
}

func (f *FailingActors) Stop() {
}

func (f *FailingActors) GetState(ctx context.Context, req *actors.GetStateRequest) (*actors.StateResponse, error) {
	return nil, nil
}

func (f *FailingActors) TransactionalStateOperation(ctx context.Context, req *actors.TransactionalRequest) error {
	return nil
}

func (f *FailingActors) GetReminder(ctx context.Context, req *actors.GetReminderRequest) (*actors.Reminder, error) {
	return nil, nil
}

func (f *FailingActors) CreateReminder(ctx context.Context, req *actors.CreateReminderRequest) error {
	return nil
}

func (f *FailingActors) DeleteReminder(ctx context.Context, req *actors.DeleteReminderRequest) error {
	return nil
}

func (f *FailingActors) RenameReminder(ctx context.Context, req *actors.RenameReminderRequest) error {
	return nil
}

func (f *FailingActors) CreateTimer(ctx context.Context, req *actors.CreateTimerRequest) error {
	return nil
}

func (f *FailingActors) DeleteTimer(ctx context.Context, req *actors.DeleteTimerRequest) error {
	return nil
}

func (f *FailingActors) IsActorHosted(ctx context.Context, req *actors.ActorHostedRequest) bool {
	return true
}

func (f *FailingActors) GetActiveActorsCount(ctx context.Context) []actors.ActiveActorsCount {
	return []actors.ActiveActorsCount{}
}
