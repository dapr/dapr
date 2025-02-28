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

package jobs

import (
	"context"
	"testing"

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
	suite.Register(new(schedule))
}

type schedule struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (s *schedule) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)

	srv := app.New(t)

	s.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.daprd),
	}
}

func (s *schedule) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test",
			Schedule: ptr.Of("1 2 3 4 5 6"),
		},
	})
	require.NoError(t, err)
}
