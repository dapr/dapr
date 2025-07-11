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

package metrics

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(jobsundeliveredtotal))
}

type jobsundeliveredtotal struct {
	actors *actors.Actors
}

func (j *jobsundeliveredtotal) Setup(t *testing.T) []framework.Option {
	j.actors = actors.New(t,
		actors.WithActorTypes(),
		actors.WithHandler("/job/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(j.actors),
	}
}

//nolint:testifylint
func (j *jobsundeliveredtotal) Run(t *testing.T, ctx context.Context) {
	j.actors.WaitUntilRunning(t, ctx)

	client := j.actors.Scheduler().Client(t, ctx)
	for i := range 5 {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{DueTime: ptr.Of("0s")},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "foo",
				Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
				},
			},
		})
		require.NoError(t, err)
	}

	for i := range 3 {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{DueTime: ptr.Of("0s")},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "foo",
				Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id:   "actor",
							Type: "actorType",
						},
					},
				},
			},
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := j.actors.Scheduler().MetricsWithLabels(t, ctx)
		total, ok := metrics.Metrics["dapr_scheduler_jobs_undelivered_total"]
		if !assert.True(c, ok) {
			return
		}
		assert.Equal(c, 5.0, total["type=job"])
		assert.Equal(c, 3.0, total["type=actor"])
	}, time.Second*15, time.Millisecond*10)
}
