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

package appcallback

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(duetime))
}

type duetime struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (d *duetime) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)

	d.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			d.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	d.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(d.scheduler, srv, d.daprd),
	}
}

func (d *duetime) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	client := d.daprd.GRPCClient(t, ctx)

	req := &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test",
			Schedule: ptr.Of("@every 1m"),
			DueTime:  ptr.Of("0s"),
		},
	}
	_, err := client.ScheduleJobAlpha1(ctx, req)
	require.NoError(t, err)

	select {
	case job := <-d.jobChan:
		assert.NotNil(t, job)
		assert.Equal(t, "job/test", job.GetMethod())

	case <-time.After(time.Second * 3):
		assert.Fail(t, "timed out waiting for triggered job")
	}
}
