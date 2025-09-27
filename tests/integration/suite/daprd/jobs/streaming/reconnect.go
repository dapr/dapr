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
	"runtime"
	"sync/atomic"
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
	suite.Register(new(reconnect))
}

type reconnect struct {
	daprd      *daprd.Daprd
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler

	jobCalled atomic.Uint64
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		// TODO: investigate why this test fails on Windows
		t.Skip("Skip due to Windows specific error on loss of connection")
	}
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			r.jobCalled.Add(1)
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	r.scheduler1 = scheduler.New(t)
	r.scheduler2 = scheduler.New(t,
		scheduler.WithID(r.scheduler1.ID()),
		scheduler.WithEtcdClientPort(r.scheduler1.EtcdClientPort()),
		scheduler.WithInitialCluster(r.scheduler1.InitialCluster()),
		scheduler.WithDataDir(r.scheduler1.DataDir()),
		scheduler.WithPort(r.scheduler1.Port()),
	)

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler1.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, r.scheduler1, r.daprd),
	}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.scheduler1.WaitUntilRunning(t, ctx)
	r.scheduler1.WaitUntilLeadership(t, ctx, 1)
	r.daprd.WaitUntilRunning(t, ctx)

	_, err := r.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test",
			Schedule: ptr.Of("@every 1s"),
		},
	})
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, r.jobCalled.Load())
	}, time.Second*5, time.Millisecond*10)

	r.scheduler1.Cleanup(t)

	called := r.jobCalled.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, called, r.jobCalled.Load())

	r.scheduler2.Run(t, ctx)
	r.scheduler2.WaitUntilRunning(t, ctx)
	r.scheduler2.WaitUntilLeadership(t, ctx, 1)
	t.Cleanup(func() { r.scheduler2.Kill(t) })

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, r.jobCalled.Load(), called)
	}, time.Second*20, time.Millisecond*10)
}
