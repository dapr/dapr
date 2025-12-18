/*
Copyright 2025 The Dapr Authors
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	ch        chan metadata.MD
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)
	a.ch = make(chan metadata.MD, 1)

	app := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *rtv1.JobEventRequest) (*rtv1.JobEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			a.ch <- md
			return &rtv1.JobEventResponse{}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "test-job-app-token"),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)
	data, err := anypb.New(structpb.NewStringValue("test message"))
	require.NoError(t, err)
	_, err = client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test-job",
			Schedule: ptr.Of("@every 1s"),
			Data:     data,
		},
	})
	require.NoError(t, err)

	select {
	case md := <-a.ch:
		tokens := md.Get("dapr-api-token")
		assert.NotEmpty(t, tokens)
		assert.Equal(t, "test-job-app-token", tokens[0])
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for job event to be delivered to app")
	}
}
