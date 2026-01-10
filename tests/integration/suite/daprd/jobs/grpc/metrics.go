/*
Copyright 2026 The Dapr Authors
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
	"context"
	nethttp "net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	corev1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(metrics))
}

type metrics struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			switch path.Base(r.URL.Path) {
			case "success":
				w.WriteHeader(nethttp.StatusOK)
			case "nonretriablefailure":
				w.WriteHeader(nethttp.StatusNotFound)
			default:
				w.WriteHeader(nethttp.StatusInternalServerError)
			}
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(m.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("my_app"),
	)

	return []framework.Option{
		framework.WithProcesses(m.scheduler, m.daprd, app),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)

	data, _ := anypb.New(wrapperspb.Bytes([]byte("hello world")))

	t.Run("successful trigger", func(t *testing.T) {
		_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     "success",
				DueTime:  ptr.Of("0s"),
				Data:     data,
				Repeats:  ptr.Of(uint32(3)),
				Schedule: ptr.Of("@every 0s"),
			},
		})
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 3, int(metrics["dapr_component_job_success_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("non-retriable failed trigger", func(t *testing.T) {
		_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:    "nonretriablefailure",
				DueTime: ptr.Of("0s"),
				Data:    data,
				FailurePolicy: &corev1pb.JobFailurePolicy{
					Policy: &corev1pb.JobFailurePolicy_Constant{
						Constant: &corev1pb.JobFailurePolicyConstant{
							Interval:   durationpb.New(time.Second),
							MaxRetries: ptr.Of(uint32(3)),
						},
					},
				},
			},
		})
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 1, int(metrics["dapr_component_job_failure_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("retriable failed trigger", func(t *testing.T) {
		_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:    "retriablefailure",
				DueTime: ptr.Of("0s"),
				Data:    data,
				FailurePolicy: &corev1pb.JobFailurePolicy{
					Policy: &corev1pb.JobFailurePolicy_Constant{
						Constant: &corev1pb.JobFailurePolicyConstant{
							Interval:   durationpb.New(time.Second),
							MaxRetries: ptr.Of(uint32(3)),
						},
					},
				},
			},
		})
		require.NoError(t, err)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 4, int(metrics["dapr_component_job_failure_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})
}
