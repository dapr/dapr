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

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nodata))
}

// nodata tests a job scheduled without data, over either protocol, is delivered
// to an HTTP app with an empty body.
type nodata struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	bodyCh    chan []byte
}

func (n *nodata) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)

	n.bodyCh = make(chan []byte, 1)
	srv := app.New(t,
		app.WithHandlerFunc("/job/", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			n.bodyCh <- body
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(n.scheduler, srv, n.daprd),
	}
}

func (n *nodata) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	expectEmptyBody := func(t *testing.T) {
		t.Helper()
		select {
		case body := <-n.bodyCh:
			assert.Empty(t, body)
		case <-time.After(time.Second * 10):
			assert.Fail(t, "timed out waiting for triggered job")
		}
	}

	t.Run("scheduled over HTTP", func(t *testing.T) {
		postURL := fmt.Sprintf("http://localhost:%d/v1.0/jobs/nodata-http", n.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL,
			strings.NewReader(`{"due_time": "0s"}`))
		require.NoError(t, err)

		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusNoContent, resp.StatusCode)

		expectEmptyBody(t)
	})

	t.Run("scheduled over gRPC", func(t *testing.T) {
		_, err := n.daprd.GRPCClient(t, ctx).ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{Name: "nodata-grpc", DueTime: new("0s")},
		})
		require.NoError(t, err)

		expectEmptyBody(t)
	})
}
