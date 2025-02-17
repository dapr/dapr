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

package metrics

import (
	"bytes"
	"context"
	"sync/atomic"
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
	suite.Register(new(jobstriggered))
}

type jobstriggered struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler

	jobChan            chan *runtimev1pb.JobEventRequest
	jobstriggeredCount atomic.Int32
}

func (j *jobstriggered) Setup(t *testing.T) []framework.Option {
	j.scheduler = scheduler.New(t)

	j.jobstriggeredCount.Store(0)
	j.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			j.jobstriggeredCount.Add(1)
			j.jobChan <- in
			return &runtimev1pb.JobEventResponse{}, nil
		}),
	)

	j.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(j.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(j.scheduler, srv, j.daprd),
	}
}

func (j *jobstriggered) Run(t *testing.T, ctx context.Context) {
	j.scheduler.WaitUntilRunning(t, ctx)
	j.daprd.WaitUntilRunning(t, ctx)

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

	client := j.daprd.GRPCClient(t, ctx)
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			startTime := time.Now()
			_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
				Job: &runtimev1pb.Job{
					Name:    name,
					DueTime: ptr.Of("0s"),
					Data:    test.data(t),
				},
			})
			require.NoError(t, err)

			select {
			case job := <-j.jobChan:
				receivedJobElapsed := time.Since(startTime).Milliseconds()
				assert.NotNil(t, job)
				assert.Equal(t, "job/"+name, job.GetMethod())
				assert.Equal(t, commonv1pb.HTTPExtension_POST, job.GetHttpExtension().GetVerb())

				assert.EventuallyWithT(t, func(c *assert.CollectT) {
					metrics := j.scheduler.Metrics(c, ctx).All()
					assert.Equal(c, int(j.jobstriggeredCount.Load()), int(metrics["dapr_scheduler_jobs_triggered_total"]))

					// with duration metrics, the following metrics can be found:
					// dapr_scheduler_trigger_duration_total_bucket
					// dapr_scheduler_trigger_duration_total_sum
					// dapr_scheduler_trigger_latency_count
					avgTriggerLatency := metrics["dapr_scheduler_trigger_latency_sum"] / metrics["dapr_scheduler_trigger_latency_count"]
					assert.Equal(c, int(j.jobstriggeredCount.Load()), int(metrics["dapr_scheduler_trigger_latency_count"]))

					// ensure the trigger duration is less than 1 second (1000 milliseconds)
					assert.Less(c, avgTriggerLatency, float64(1000), "Trigger duration should be less than 1 second")

					grace := 1000
					// triggered time should be less than the total round trip time of a job being scheduled and sent back to the app
					assert.LessOrEqual(c, int64(avgTriggerLatency), receivedJobElapsed+int64(grace), "Trigger time should be less than the total elapsed time to receive the scheduled job")
				}, time.Second*3, 10*time.Millisecond)

				test.exp(t, job)
			case <-time.After(time.Second * 10):
				assert.Fail(t, "timed out waiting for triggered job")
			}
		})
	}
	assert.Equal(t, len(tests), int(j.jobstriggeredCount.Load()))
}
