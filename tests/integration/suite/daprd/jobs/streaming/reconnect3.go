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
	"fmt"
	"runtime"
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
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(reconnect3))
}

type reconnect3 struct {
	daprd      *daprd.Daprd
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler
	scheduler4 *scheduler.Scheduler

	jobCalledMap map[string]struct{}
	lock         sync.Mutex
}

func (r *reconnect3) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		// TODO: investigate why this test fails on Windows
		t.Skip("Skip due to Windows specific error on loss of connection")
	}

	r.jobCalledMap = make(map[string]struct{})
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			r.lock.Lock()
			r.jobCalledMap[in.GetName()] = struct{}{}
			r.lock.Unlock()
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	r.scheduler1 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPort(port4))...)
	r.scheduler2 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPort(port5))...)
	r.scheduler3 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPort(port6))...)

	r.scheduler4 = scheduler.New(t,
		scheduler.WithID(r.scheduler2.ID()),
		scheduler.WithEtcdClientPort(port5),
		scheduler.WithInitialCluster(r.scheduler2.InitialCluster()),
		scheduler.WithDataDir(r.scheduler2.DataDir()),
		scheduler.WithPort(r.scheduler2.Port()),
	)

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler1.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(srv, r.scheduler1, r.scheduler2, r.scheduler3, r.daprd),
	}
}

func (r *reconnect3) Run(t *testing.T, ctx context.Context) {
	r.scheduler1.WaitUntilRunning(t, ctx)
	r.scheduler2.WaitUntilRunning(t, ctx)
	r.scheduler3.WaitUntilRunning(t, ctx)

	r.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.daprd.GetMetaScheduler(c, ctx).GetConnectedAddresses(), 3)
	}, time.Second*10, time.Millisecond*10)

	for i := range 10 {
		_, err := r.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     strconv.Itoa(i),
				Schedule: ptr.Of("@every 1s"),
			},
		})
		require.NoError(t, err)
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r.lock.Lock()
		assert.Len(c, r.jobCalledMap, 10)
		r.lock.Unlock()
	}, time.Second*5, time.Millisecond*10)

	r.scheduler2.Kill(t)

	time.Sleep(time.Second * 5)

	r.lock.Lock()
	r.jobCalledMap = make(map[string]struct{})
	r.lock.Unlock()

	r.scheduler4.Run(t, ctx)
	r.scheduler4.WaitUntilRunning(t, ctx)
	r.scheduler4.WaitUntilLeadership(t, ctx, 3)
	t.Cleanup(func() { r.scheduler4.Kill(t) })

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		r.lock.Lock()
		assert.Len(c, r.jobCalledMap, 10)
		r.lock.Unlock()
	}, time.Second*20, time.Millisecond*10)
}
