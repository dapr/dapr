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

package grpc

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(api))
}

type api struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (a *api) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)

	a.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			a.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, srv, a.daprd),
	}
}

func (a *api) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	tests := map[string]struct {
		data func(t *testing.T) *anypb.Any
		exp  func(t *testing.T, job *runtimev1pb.JobEventRequest)
	}{
		"expr": {
			data: func(*testing.T) *anypb.Any {
				t.Helper()
				str, err := structpb.NewStruct(map[string]any{
					"expression": "val",
				})
				require.NoError(t, err)
				data, err := anypb.New(structpb.NewStructValue(str))
				require.NoError(t, err)
				return data
			},
			exp: func(t *testing.T, job *runtimev1pb.JobEventRequest) {
				t.Helper()
				str, err := structpb.NewStruct(map[string]any{
					"expression": "val",
				})
				require.NoError(t, err)
				data, err := anypb.New(structpb.NewStructValue(str))
				require.NoError(t, err)
				assert.Equal(t, data, job.GetData())
			},
		},
		"bytes": {
			data: func(t *testing.T) *anypb.Any {
				t.Helper()
				anyB, err := anypb.New(wrapperspb.Bytes([]byte("hello world")))
				require.NoError(t, err)
				return anyB
			},
			exp: func(t *testing.T, job *runtimev1pb.JobEventRequest) {
				t.Helper()
				assert.Equal(t, "type.googleapis.com/google.protobuf.BytesValue", job.GetData().GetTypeUrl())
				assert.Equal(t, []byte("hello world"), bytes.TrimSpace((job.GetData().GetValue())))
				var b wrapperspb.BytesValue
				require.NoError(t, job.GetData().UnmarshalTo(&b))
				assert.Equal(t, []byte("hello world"), b.GetValue())
			},
		},
		"ping": {
			data: func(t *testing.T) *anypb.Any {
				anyB, err := anypb.New(&proto.PingResponse{Value: "pong", Counter: 123})
				require.NoError(t, err)
				return anyB
			},
			exp: func(t *testing.T, job *runtimev1pb.JobEventRequest) {
				assert.Equal(t, "type.googleapis.com/dapr.io.testproto.PingResponse", job.GetData().GetTypeUrl())
				var ping proto.PingResponse
				require.NoError(t, job.GetData().UnmarshalTo(&ping))
				assert.Equal(t, "pong", ping.GetValue())
				assert.Equal(t, int32(123), ping.GetCounter())
			},
		},
	}

	client := a.daprd.GRPCClient(t, ctx)
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
				Job: &runtimev1pb.Job{
					Name:    name,
					DueTime: ptr.Of("0s"),
					Data:    test.data(t),
				},
			})
			require.NoError(t, err)

			select {
			case job := <-a.jobChan:
				assert.NotNil(t, job)
				assert.Equal(t, "job/"+name, job.GetMethod())
				assert.Equal(t, commonv1pb.HTTPExtension_POST, job.GetHttpExtension().GetVerb())
				test.exp(t, job)
			case <-time.After(time.Second * 10):
				assert.Fail(t, "timed out waiting for triggered job")
			}
		})
	}
}
