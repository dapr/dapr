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
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

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
	suite.Register(new(remove))
}

type remove struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	triggered atomic.Int64

	etcdPort int
}

func (r *remove) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 2)
	port1 := fp.Port(t)
	port2 := fp.Port(t)
	r.etcdPort = port2
	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(r.etcdPort),
	}
	r.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithInitialClusterPorts(port1),
		scheduler.WithEtcdClientPorts(clientPorts),
	)

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			r.triggered.Add(1)
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(r.scheduler, app, r.daprd),
	}
}

func (r *remove) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)

	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%d", r.etcdPort)},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, etcdClient.Close()) })

	resp, err := etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Count)

	req := &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test",
			Schedule: ptr.Of("@every 20s"),
			DueTime:  ptr.Of("0s"),
		},
	}
	_, err = client.ScheduleJobAlpha1(ctx, req)
	require.NoError(t, err)

	resp, err = etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Count)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.triggered.Load())
	}, 30*time.Second, 10*time.Millisecond)

	_, err = client.DeleteJobAlpha1(ctx, &runtimev1pb.DeleteJobRequest{
		Name: "test",
	})
	require.NoError(t, err)

	resp, err = etcdClient.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Count)
}
