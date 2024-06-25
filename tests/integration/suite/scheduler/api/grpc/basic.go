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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
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
					Schedule: nil,
				},
			},
			{
				Job: &rtv1.Job{
					Name: "test",
					Ttl:  ptr.Of("3h"),
				},
			},
		} {
			_, err := client.ScheduleJobAlpha1(ctx, req)
			require.Error(t, err)
		}
	})

	t.Run("good request", func(t *testing.T) {
		for _, req := range []*rtv1.ScheduleJobRequest{
			{
				Job: &rtv1.Job{
					Name:     "test",
					Schedule: ptr.Of("@daily"),
					Repeats:  ptr.Of(uint32(1)),
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test1",
					Schedule: ptr.Of("@daily"),
					Data: &anypb.Any{
						Value: []byte("test"),
					},
					Repeats: ptr.Of(uint32(1)),
				},
			},
			{
				Job: &rtv1.Job{
					Name:     "test1",
					Schedule: ptr.Of("@daily"),
					Data: &anypb.Any{
						Value: []byte("test"),
					},
					Repeats: ptr.Of(uint32(1)),
					DueTime: ptr.Of("0h0m9s0ms"),
					Ttl:     ptr.Of("20s"),
				},
			},
		} {
			_, err := client.ScheduleJobAlpha1(ctx, req)
			require.NoError(t, err)
		}
	})
}
