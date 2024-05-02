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
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpcallback))
}

type httpcallback struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (h *httpcallback) Setup(t *testing.T) []framework.Option {
	h.scheduler = scheduler.New(t)

	h.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithHandlerFunc("/dapr/receiveJobs/test", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Error reading request body", http.StatusInternalServerError)
				return
			}

			var jobEventRequest runtimev1pb.JobEventRequest
			if err := json.Unmarshal(body, &jobEventRequest.Data); err != nil {
				http.Error(w, "Error decoding JSON", http.StatusBadRequest)
				return
			}
			jobEventRequest.Method = r.URL.String()
			h.jobChan <- &jobEventRequest

			w.WriteHeader(http.StatusOK)
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(h.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(h.scheduler, srv, h.daprd),
	}
}

func (h *httpcallback) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := h.daprd.GRPCClient(t, ctx)

	t.Run("app receives triggered job", func(t *testing.T) {
		h.receiveJob(t, ctx, client)
	})
}

func (h *httpcallback) receiveJob(t *testing.T, ctx context.Context, client runtimev1pb.DaprClient) {
	t.Helper()

	req := &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     "test",
			Schedule: "@every 1s",
			Repeats:  1,
			Data: &anypb.Any{
				TypeUrl: "type.googleapis.com/google.type.Expr",
				Value:   []byte(`{"expression": "val"}`),
			},
		},
	}
	_, err := client.ScheduleJob(ctx, req)
	require.NoError(t, err)

	select {
	case job := <-h.jobChan:
		assert.NotNil(t, job)
		assert.NotNil(t, job.GetData())
		assert.Equal(t, "/dapr/receiveJobs/test", job.GetMethod())
		assert.Equal(t, `{"expression": "val"}`, string(job.GetData().GetValue()))
		assert.Equal(t, "type.googleapis.com/google.type.Expr", job.GetData().GetTypeUrl())

	case <-time.After(time.Second * 3):
		assert.Fail(t, "timed out waiting for triggered job")
	}
}
