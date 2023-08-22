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

package actors

import (
	"bytes"
	"context"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	"github.com/dapr/kit/logger"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

var testActorsRuntime *actorsRuntime

func init() {
	testActorsRuntime = newTestActorsRuntime()
	err := testActorsRuntime.Init(context.Background())
	if err != nil {
		panic(err)
	}
	log.SetOutputLevel(logger.FatalLevel)
}

func FuzzActorsRuntime(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, apiCall int) {
		ff := fuzz.NewConsumer(data)
		switch apiCall % 10 {
		case 0:
			r := &RenameReminderRequest{}
			ff.GenerateStruct(r)
			_ = testActorsRuntime.RenameReminder(context.Background(), r)
		case 1:
			r := &CreateTimerRequest{}
			ff.GenerateStruct(r)
			_ = testActorsRuntime.CreateTimer(context.Background(), r)
		case 2:
			ff.AllowUnexportedFields()
			ir := &commonv1pb.InvokeRequest{}
			ff.GenerateStruct(ir)
			md := make(map[string][]string)
			ff.FuzzMap(&md)
			data2, err := ff.GetBytes()
			if err != nil {
				return
			}
			data3, err := ff.GetBytes()
			if err != nil {
				return
			}
			actorType, err := ff.GetString()
			if err != nil {
				return
			}
			actorID, err := ff.GetString()
			if err != nil {
				return
			}
			r := invokev1.FromInvokeRequestMessage(ir).
				WithRawData(bytes.NewReader(data2)).
				WithRawDataBytes(data3).
				WithActor(actorType, actorID).
				WithMetadata(md)

			_, _ = testActorsRuntime.Call(context.Background(), r)
		case 3:
			r := &GetStateRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.GetState(context.Background(), r)
		case 4:
			r := &TransactionalRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.TransactionalStateOperation(context.Background(), r)
		case 5:
			r := &ActorHostedRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.IsActorHosted(context.Background(), r)
		case 6:
			r := &CreateReminderRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.CreateReminder(context.Background(), r)
		case 7:
			r := &CreateTimerRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.CreateTimer(context.Background(), r)
		case 8:
			r := &DeleteTimerRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.DeleteTimer(context.Background(), r)
		case 9:
			r := &RenameReminderRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.RenameReminder(context.Background(), r)
		}
	})
}
