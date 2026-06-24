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

package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/ptr"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(specialchars))
}

// specialchars asserts that a job whose name contains characters such as '|'
// and '@' can be scheduled, triggered and fetched.
type specialchars struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (s *specialchars) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)

	s.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			s.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, srv, s.daprd),
	}
}

func (s *specialchars) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	const name = "my|job@name"

	_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     name,
			Schedule: ptr.Of("@daily"),
			DueTime:  ptr.Of("0s"),
		},
	})
	require.NoError(t, err)

	select {
	case job := <-s.jobChan:
		assert.Equal(t, "job/"+name, job.GetMethod())
	case <-time.After(time.Second * 3):
		assert.Fail(t, "timed out waiting for triggered job")
	}

	got, err := client.GetJobAlpha1(ctx, &runtimev1pb.GetJobRequest{Name: name})
	require.NoError(t, err)
	assert.Equal(t, name, got.GetJob().GetName())
}
