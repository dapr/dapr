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

package grpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(api))
}

type api struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

type jobData struct {
	TypeURL string `json:"type_url"`
	Value   string `json:"value"`
}

func (a *api) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)

	a.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			a.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, srv, a.daprd),
	}
}

func (a *api) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)

	t.Run("app receives triggered job", func(t *testing.T) {
		req := &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     "test",
				Schedule: ptr.Of("@every 1s"), Repeats: ptr.Of(uint32(1)),
				Data: &anypb.Any{
					TypeUrl: "type.googleapis.com/google.type.Expr",
					Value:   []byte(`{"expression": "val"}`),
				},
			},
		}
		_, err := client.ScheduleJobAlpha1(ctx, req)
		require.NoError(t, err)

		select {
		case job := <-a.jobChan:
			assert.NotNil(t, job)
			assert.Equal(t, "job/test", job.GetMethod())

			var data jobData
			dataBytes := job.GetData().GetValue()

			err := json.Unmarshal(dataBytes, &data)
			require.NoError(t, err)

			decodedValue, err := base64.StdEncoding.DecodeString(data.Value)
			require.NoError(t, err)

			actualVal := strings.TrimSpace(string(decodedValue))
			assert.Equal(t, `{"expression": "val"}`, actualVal)

		case <-time.After(time.Second * 3):
			assert.Fail(t, "timed out waiting for triggered job")
		}
	})
}
