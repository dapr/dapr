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

package streaming

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	daprdA *daprd.Daprd
	daprdB *daprd.Daprd
	daprdC *daprd.Daprd

	schedulers *cluster.Cluster

	lock        sync.Mutex
	triggered   map[string]int
	daprdCalled map[int]bool
}

func (h *ha) Setup(t *testing.T) []framework.Option {
	h.schedulers = cluster.New(t, cluster.WithCount(3))
	h.triggered = make(map[string]int)
	h.daprdCalled = make(map[int]bool)

	app1 := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.triggered[in.GetName()]++
			h.daprdCalled[1] = true
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)
	app2 := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.triggered[in.GetName()]++
			h.daprdCalled[2] = true
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)
	app3 := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.triggered[in.GetName()]++
			h.daprdCalled[3] = true
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	h.daprdA = daprd.New(t,
		daprd.WithSchedulerAddresses(h.schedulers.Addresses()[0]),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app1.Port(t)),
	)

	h.daprdB = daprd.New(t,
		daprd.WithSchedulerAddresses(h.schedulers.Addresses()[0]),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app2.Port(t)),
		daprd.WithAppID(h.daprdA.AppID()),
	)

	h.daprdC = daprd.New(t,
		daprd.WithSchedulerAddresses(h.schedulers.Addresses()[0]),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app3.Port(t)),
		daprd.WithAppID(h.daprdA.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(h.schedulers, app1, app2, app3, h.daprdA, h.daprdB, h.daprdC),
	}
}

func (h *ha) Run(t *testing.T, ctx context.Context) {
	h.schedulers.WaitUntilRunning(t, ctx)
	h.daprdA.WaitUntilRunning(t, ctx)
	h.daprdB.WaitUntilRunning(t, ctx)
	h.daprdC.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, daprd := range []*daprd.Daprd{h.daprdA, h.daprdB, h.daprdC} {
			resp, err := daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(runtimev1pb.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetScheduler().GetConnectedAddresses(), 3)
		}
	}, time.Second*10, time.Millisecond*10)

	for i := range 150 {
		_, err := h.daprdA.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name: strconv.Itoa(i), Schedule: ptr.Of("@every 1s"),
				DueTime: ptr.Of(time.Now().Format(time.RFC3339)),
				Repeats: ptr.Of(uint32(3)),
			},
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		h.lock.Lock()
		assert.Len(c, h.triggered, 150)
		h.lock.Unlock()
	}, 10*time.Second, 10*time.Millisecond)
	h.lock.Lock()
	assert.Len(t, h.daprdCalled, 3)
	h.lock.Unlock()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		h.lock.Lock()
		for i := range 150 {
			assert.Equal(c, 3, h.triggered[strconv.Itoa(i)])
		}
		h.lock.Unlock()
	}, 20*time.Second, 10*time.Millisecond)
}
