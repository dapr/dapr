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

package http

import (
	"bytes"
	"context"
	"encoding/json"
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
	suite.Register(new(api))
}

type api struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (a *api) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)

	a.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithHandlerFunc("/job/test", func(w http.ResponseWriter, r *http.Request) {
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
			a.jobChan <- &jobEventRequest

			w.WriteHeader(http.StatusOK)
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, srv, a.daprd),
	}
}

func (a *api) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	body := `{
"schedule": "@every 1s",
"repeats": 1,
"data": {
	"@type": "type.googleapis.com/google.protobuf.StringValue",
	"value": "\"someData\""
}}`

	url := fmt.Sprintf("http://%s/v1.0-alpha1/jobs/test", a.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	select {
	case job := <-a.jobChan:
		assert.NotNil(t, job)
		assert.NotNil(t, job.GetData())
		assert.Equal(t, "/job/test", job.GetMethod())
		assert.Equal(t, []byte(`"someData"`), bytes.TrimSpace(job.GetData().GetValue()))
		assert.Equal(t, "type.googleapis.com/google.protobuf.StringValue", job.GetData().GetTypeUrl())

	case <-time.After(time.Second * 3):
		assert.Fail(t, "timed out waiting for triggered job")
	}
}
