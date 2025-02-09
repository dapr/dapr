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

package actors

import (
	"context"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"

	"github.com/dapr/kit/logger"
)

func init() {
	log.SetOutputLevel(logger.FatalLevel)
}

// This fuzz test tests different methods of the Actors runtime.
// The test will choose a method in each iteration and then
// generate the necessary objects that the method accepts.
func FuzzActorsRuntime(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, apiCall int) {
		var testActorsRuntime *actorsRuntime
		testActorsRuntime = newTestActorsRuntime(t)
		err := testActorsRuntime.Init(context.Background())
		if err != nil {
			panic(err)
		}
		ff := fuzz.NewConsumer(data)
		switch apiCall % 8 {
		case 0:
			r := &CreateTimerRequest{}
			ff.GenerateStruct(r)
			_ = testActorsRuntime.CreateTimer(context.Background(), r)
		case 1:
			ff.AllowUnexportedFields()
			req := &internalsv1pb.InternalInvokeRequest{}
			ff.GenerateStruct(req)

			_, _ = testActorsRuntime.Call(context.Background(), req)
		case 2:
			r := &GetStateRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.GetState(context.Background(), r)
		case 3:
			r := &TransactionalRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.TransactionalStateOperation(context.Background(), r)
		case 4:
			r := &ActorHostedRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.IsActorHosted(context.Background(), r)
		case 5:
			r := &CreateReminderRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.CreateReminder(context.Background(), r)
		case 6:
			r := &CreateTimerRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.CreateTimer(context.Background(), r)
		case 7:
			r := &DeleteTimerRequest{}
			err := ff.GenerateStruct(r)
			if err != nil {
				return
			}
			testActorsRuntime.DeleteTimer(context.Background(), r)
		}
	})
}
