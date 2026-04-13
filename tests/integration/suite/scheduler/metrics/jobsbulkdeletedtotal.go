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

package metrics

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(jobsbulkdeletedtotal))
}

type jobsbulkdeletedtotal struct {
	scheduler *scheduler.Scheduler
}

func (j *jobsbulkdeletedtotal) Setup(t *testing.T) []framework.Option {
	j.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(j.scheduler),
	}
}

func (j *jobsbulkdeletedtotal) Run(t *testing.T, ctx context.Context) {
	j.scheduler.WaitUntilRunning(t, ctx)

	client := j.scheduler.Client(t, ctx)

	for i := range 5 {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "job-" + strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{Schedule: new("@every 100s")},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "appid1",
				Namespace: "namespace",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: new(schedulerv1pb.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
	}

	for i := range 5 {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "job-" + strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{Schedule: new("@every 100s")},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "appid2",
				Namespace: "namespace",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: new(schedulerv1pb.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
	}

	_, err := client.DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "appid1",
			Namespace: "namespace",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: new(schedulerv1pb.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)

	_, err = client.DeleteByNamePrefix(ctx, &schedulerv1pb.DeleteByNamePrefixRequest{
		NamePrefix: "job-",
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "appid2",
			Namespace: "namespace",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: new(schedulerv1pb.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := j.scheduler.Metrics(t, ctx).All()
		assert.Equal(c, 2.0, metrics["dapr_scheduler_jobs_bulk_deleted_total"])
	}, time.Second*15, time.Millisecond*10)
}
