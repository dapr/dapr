/*
Copyright 2025 The Dapr Authors
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

package loadbalance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(single))
}

type single struct {
	daprdA    *daprd.Daprd
	daprdB    *daprd.Daprd
	daprdC    *daprd.Daprd
	scheduler *scheduler.Scheduler

	called     atomic.Int64
	totalCalls atomic.Int64
}

func (s *single) Setup(t *testing.T) []framework.Option {
	s.called.Store(0)
	s.totalCalls.Store(0)

	var hasCalledA, hasCalledB, hasCalledC atomic.Bool
	srvA := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			s.totalCalls.Add(1)
			if hasCalledA.CompareAndSwap(false, true) {
				s.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)
	srvB := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			s.totalCalls.Add(1)
			if hasCalledB.CompareAndSwap(false, true) {
				s.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)
	srvC := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			s.totalCalls.Add(1)
			if hasCalledC.CompareAndSwap(false, true) {
				s.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)

	s.scheduler = scheduler.New(t)

	s.daprdA = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvA.Port(t)),
	)
	s.daprdB = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvB.Port(t)),
		daprd.WithAppID(s.daprdA.AppID()),
	)
	s.daprdC = daprd.New(t,
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvC.Port(t)),
		daprd.WithAppID(s.daprdA.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(srvA, srvB, srvC, s.scheduler, s.daprdA, s.daprdB, s.daprdC),
	}
}

func (s *single) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	s.daprdA.WaitUntilRunning(t, ctx)
	s.daprdB.WaitUntilRunning(t, ctx)
	s.daprdC.WaitUntilRunning(t, ctx)

	_, err := s.daprdA.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &rtv1pb.ScheduleJobRequest{
		Job: &rtv1pb.Job{
			Name:     "job1",
			Schedule: ptr.Of("@every 1s"),
			DueTime:  ptr.Of("0s"),
			Repeats:  ptr.Of(uint32(3)),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, int64(3), s.called.Load())
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(3), s.totalCalls.Load())
}
