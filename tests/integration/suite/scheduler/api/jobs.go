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

package api

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(schedulejobs))
}

// schedulejobs tests daprd scheduling jobs against the scheduler.
type schedulejobs struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (j *schedulejobs) Setup(t *testing.T) []framework.Option {
	j.scheduler = scheduler.New(t)

	j.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(j.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(j.daprd, j.scheduler),
	}
}

func (j *schedulejobs) Run(t *testing.T, ctx context.Context) {
	j.scheduler.WaitUntilRunning(t, ctx)
	j.daprd.WaitUntilRunning(t, ctx)

	client := j.daprd.GRPCClient(t, ctx)

	t.Run("CRUD 10 jobs", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			name := "test" + strconv.Itoa(i)

			req := &rtv1.ScheduleJobRequest{
				Job: &rtv1.Job{
					Name:     name,
					Schedule: "@daily",
					Data: &anypb.Any{
						Value: []byte("test"),
					},
					Repeats: 1,
					DueTime: "0h0m9s0ms",
					Ttl:     "20s",
				},
			}

			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)

			resp, err := client.GetJob(ctx, &rtv1.GetJobRequest{Name: name})
			require.NotNil(t, resp)
			require.Equal(t, name, resp.GetJob().GetName())
			require.NoError(t, err)
		}

		resp, err := client.ListJobs(ctx, &rtv1.ListJobsRequest{
			AppId: j.daprd.AppID(),
		})
		require.NoError(t, err)
		count := len(resp.GetJobs())
		require.Equal(t, 10, count)

		for i := 1; i <= 10; i++ {
			name := "test" + strconv.Itoa(i)

			_, err = client.DeleteJob(ctx, &rtv1.DeleteJobRequest{Name: name})
			require.NoError(t, err)

			resp, nerr := client.GetJob(ctx, &rtv1.GetJobRequest{Name: name})
			require.Nil(t, resp)
			require.Error(t, nerr)
		}

		resp, err = client.ListJobs(ctx, &rtv1.ListJobsRequest{
			AppId: j.daprd.AppID(),
		})
		require.Empty(t, resp.GetJobs())
		require.NoError(t, err)
	})
}
