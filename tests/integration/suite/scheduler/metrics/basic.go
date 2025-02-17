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

package metrics

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	scheduler *scheduler.Scheduler

	idPrefix string
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	uuid, err := uuid.NewUUID()
	require.NoError(t, err)
	b.idPrefix = uuid.String()

	b.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(b.scheduler),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)
	client := b.scheduler.Client(t, ctx)

	t.Run("create 10 jobs, ensure metrics", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			name := b.idPrefix + "_" + strconv.Itoa(i)

			req := &schedulerv1.ScheduleJobRequest{
				Name: name,
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
					Repeats:  ptr.Of(uint32(1)),
					Data: &anypb.Any{
						Value: []byte(b.idPrefix),
					},
					Ttl: ptr.Of("30s"),
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
				Name: name,
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			})
			assert.NotNil(t, resp)
			require.NoError(t, err)

			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				metrics := b.scheduler.Metrics(c, ctx).All()
				assert.Equal(c, i, int(metrics["dapr_scheduler_jobs_created_total"]))
			}, time.Second*3, time.Millisecond*10)
		}
	})
}
