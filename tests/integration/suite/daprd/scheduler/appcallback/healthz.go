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
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(healthz))
}

type healthz struct {
	daprd        *daprd.Daprd
	scheduler    *scheduler.Scheduler
	jobCalled    atomic.Uint32
	appHealthy   atomic.Bool
	healthCalled atomic.Int64
}

func (h *healthz) Setup(t *testing.T) []framework.Option {
	h.scheduler = scheduler.New(t)
	h.appHealthy.Store(true)

	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1.JobEventRequest) (*rtv1.JobEventResponse, error) {
			h.jobCalled.Add(1)
			return new(rtv1.JobEventResponse), nil
		}),
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			defer h.healthCalled.Add(1)
			if h.appHealthy.Load() {
				return new(rtv1.HealthCheckResponse), nil
			}
			return nil, errors.New("app not healthy")
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(h.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
	)

	return []framework.Option{
		framework.WithProcesses(h.scheduler, srv, h.daprd),
	}
}

func (h *healthz) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := h.daprd.GRPCClient(t, ctx)

	_, err := client.ScheduleJob(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test",
			Schedule: ptr.Of("@every 1s"),
		},
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return h.jobCalled.Load() > 0
	}, time.Second*5, time.Millisecond*10)

	healthzCalled := h.healthCalled.Load()
	h.appHealthy.Store(false)

	assert.Eventually(t, func() bool {
		return h.healthCalled.Load() > healthzCalled+2
	}, time.Second*5, time.Millisecond*10)

	jobCalled := h.jobCalled.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, jobCalled, h.jobCalled.Load())

	h.appHealthy.Store(true)

	assert.Eventually(t, func() bool {
		return h.jobCalled.Load() > jobCalled
	}, time.Second*5, time.Millisecond*10)

	healthzCalled = h.healthCalled.Load()
	h.appHealthy.Store(false)

	assert.Eventually(t, func() bool {
		return h.healthCalled.Load() > healthzCalled+2
	}, time.Second*5, time.Millisecond*10)

	jobCalled = h.jobCalled.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, jobCalled, h.jobCalled.Load())
}
