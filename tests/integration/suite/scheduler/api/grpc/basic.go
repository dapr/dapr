/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	b.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(b.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(b.scheduler, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	t.Run("bad request", func(t *testing.T) {
		for _, req := range []*rtv1.ScheduleJobRequest{
			nil,
			{},
			{
				Job: &rtv1.Job{
					Name: "",
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test",
					Schedule: "",
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test",
					Schedule: "@daily",
					Repeats:  -1,
				},
			},
		} {
			_, err := client.ScheduleJob(ctx, req)
			require.Error(t, err)
		}
	})

	t.Run("good request", func(t *testing.T) {
		for _, req := range []*rtv1.ScheduleJobRequest{
			{
				Job: &rtv1.Job{
					Name:     "test",
					Schedule: "@daily",
					Data:     nil,
					Repeats:  1,
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test1",
					Schedule: "@daily",
					Data: &anypb.Any{
						Value: []byte("test"),
					},
					Repeats: 1,
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test1",
					Schedule: "@daily",
					Data: &anypb.Any{
						Value: []byte("test"),
					},
					Repeats: 1,
					DueTime: "0h0m9s0ms",
					Ttl:     "20s",
				},
			},
		} {
			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)
		}
	})
}
