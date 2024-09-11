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
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(fields))
}

type fields struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (f *fields) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)

	f.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(f.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(f.scheduler, f.daprd),
	}
}

func (f *fields) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	f.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/test", strings.NewReader(`{
"schedule": "@every 1m",
"repeats": 123,
"dueTime": "1000s",
"ttl": "123s",
"data": "hello world"
}`))

	job, err := f.daprd.GRPCClient(t, ctx).GetJobAlpha1(ctx,
		&runtimev1pb.GetJobRequest{Name: "test"},
	)
	require.NoError(t, err)
	//nolint:protogetter
	require.NotNil(t, job.Job)

	expData, err := anypb.New(structpb.NewStringValue("hello world"))
	require.NoError(t, err)

	assert.Equal(t,
		&runtimev1pb.GetJobResponse{
			Job: &runtimev1pb.Job{
				Name:     "test",
				Schedule: ptr.Of("@every 1m"),
				Repeats:  ptr.Of(uint32(123)),
				DueTime:  ptr.Of("1000s"),
				Ttl:      ptr.Of("123s"),
				Data:     expData,
			},
		},
		//nolint:protogetter
		&runtimev1pb.GetJobResponse{
			Job: &runtimev1pb.Job{
				Name:     job.Job.Name,
				Schedule: job.Job.Schedule,
				Repeats:  job.Job.Repeats,
				DueTime:  job.Job.DueTime,
				Ttl:      job.Job.Ttl,
				Data:     job.Job.Data,
			},
		},
	)
}
