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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	cluster *cluster.Cluster
}

func (h *ha) Setup(t *testing.T) []framework.Option {
	h.cluster = cluster.New(t, cluster.WithCount(3))

	return []framework.Option{
		framework.WithProcesses(h.cluster),
	}
}

func (h *ha) Run(t *testing.T, ctx context.Context) {
	h.cluster.WaitUntilRunning(t, ctx)

	client := h.cluster.Client(t, ctx)

	t.Run("CRUD 10 jobs", func(t *testing.T) {
		for i := range 10 {
			req := &schedulerv1.ScheduleJobRequest{
				Name: strconv.Itoa(i),
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
					Repeats:  ptr.Of(uint32(1)),
					Data:     &anypb.Any{Value: []byte(strconv.Itoa(i))},
					Ttl:      ptr.Of("30s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			}

			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)

			resp, err := client.GetJob(ctx, &schedulerv1.GetJobRequest{
				Name: strconv.Itoa(i),
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, "@every 20s", resp.GetJob().GetSchedule())
			assert.Equal(t, uint32(1), resp.GetJob().GetRepeats())
			assert.Equal(t, "30s", resp.GetJob().GetTtl())
			assert.Equal(t, &anypb.Any{Value: []byte(strconv.Itoa(i))}, resp.GetJob().GetData())
		}
	})

	for i := range 10 {
		_, err := client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
			Name: strconv.Itoa(i),
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)

		resp, err := client.GetJob(ctx, &schedulerv1.GetJobRequest{
			Name: strconv.Itoa(i),
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.ErrorContains(t, err, "job not found")
		assert.Nil(t, resp.GetJob())
	}
}
