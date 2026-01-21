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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(success))
}

type success struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (s *success) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppID("my_app"),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.daprd, app),
	}
}

func (s *success) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	data, _ := anypb.New(wrapperspb.Bytes([]byte("hello world")))

	t.Run("successful job trigger", func(t *testing.T) {
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
			metrics := s.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 3, int(metrics["dapr_component_job_success_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})
}
