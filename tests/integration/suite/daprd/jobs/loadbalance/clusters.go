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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(clusters))
}

type clusters struct {
	daprdA     *daprd.Daprd
	daprdB     *daprd.Daprd
	daprdC     *daprd.Daprd
	schedulers *cluster.Cluster

	called     atomic.Int64
	totalCalls atomic.Int64
}

func (c *clusters) Setup(t *testing.T) []framework.Option {
	c.called.Store(0)
	c.totalCalls.Store(0)

	var hasCalledA, hasCalledB, hasCalledC atomic.Bool
	srvA := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			c.totalCalls.Add(1)
			if hasCalledA.CompareAndSwap(false, true) {
				c.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)
	srvB := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			c.totalCalls.Add(1)
			if hasCalledB.CompareAndSwap(false, true) {
				c.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)
	srvC := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1pb.JobEventRequest) (*rtv1pb.JobEventResponse, error) {
			c.totalCalls.Add(1)
			if hasCalledC.CompareAndSwap(false, true) {
				c.called.Add(1)
			}
			return new(rtv1pb.JobEventResponse), nil
		}),
	)

	c.schedulers = cluster.New(t, cluster.WithCount(3))

	c.daprdA = daprd.New(t,
		daprd.WithSchedulerAddresses(c.schedulers.Addresses()...),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvA.Port(t)),
	)
	c.daprdB = daprd.New(t,
		daprd.WithSchedulerAddresses(c.schedulers.Addresses()...),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvB.Port(t)),
		daprd.WithAppID(c.daprdA.AppID()),
	)
	c.daprdC = daprd.New(t,
		daprd.WithSchedulerAddresses(c.schedulers.Addresses()...),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srvC.Port(t)),
		daprd.WithAppID(c.daprdA.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(srvA, srvB, srvC, c.schedulers, c.daprdA, c.daprdB, c.daprdC),
	}
}

func (c *clusters) Run(t *testing.T, ctx context.Context) {
	c.schedulers.WaitUntilRunning(t, ctx)

	c.daprdA.WaitUntilRunning(t, ctx)
	c.daprdB.WaitUntilRunning(t, ctx)
	c.daprdC.WaitUntilRunning(t, ctx)

	_, err := c.daprdA.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &rtv1pb.ScheduleJobRequest{
		Job: &rtv1pb.Job{
			Name:     "job1",
			Schedule: ptr.Of("@every 1s"),
			DueTime:  ptr.Of("0s"),
			Repeats:  ptr.Of(uint32(3)),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, int64(3), c.called.Load())
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(3), c.totalCalls.Load())
}
