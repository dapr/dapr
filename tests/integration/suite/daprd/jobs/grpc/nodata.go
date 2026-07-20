/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nodata))
}

// nodata tests a job scheduled without data, over either protocol, is
// delivered to a gRPC app.
type nodata struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobCh     chan *runtimev1pb.JobEventRequest
}

func (n *nodata) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)

	n.jobCh = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			n.jobCh <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(n.scheduler, srv, n.daprd),
	}
}

func (n *nodata) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	expectTrigger := func(t *testing.T, name string) {
		t.Helper()
		select {
		case job := <-n.jobCh:
			assert.Equal(t, "job/"+name, job.GetMethod())
		case <-time.After(time.Second * 10):
			assert.Fail(t, "timed out waiting for triggered job")
		}
	}

	t.Run("scheduled over gRPC", func(t *testing.T) {
		_, err := n.daprd.GRPCClient(t, ctx).ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{Name: "nodata-grpc", DueTime: new("0s")},
		})
		require.NoError(t, err)

		expectTrigger(t, "nodata-grpc")
	})

	t.Run("scheduled over HTTP", func(t *testing.T) {
		postURL := fmt.Sprintf("http://localhost:%d/v1.0/jobs/nodata-http", n.daprd.HTTPPort())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, postURL,
			strings.NewReader(`{"due_time": "0s"}`))
		require.NoError(t, err)

		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)

		expectTrigger(t, "nodata-http")
	})
}
