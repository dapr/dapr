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

package api

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(deletebymetadata))
}

type deletebymetadata struct {
	scheduler *scheduler.Scheduler
}

func (d *deletebymetadata) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *deletebymetadata) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	client := d.scheduler.Client(t, ctx)

	t.Run("jobs", func(t *testing.T) {
		for i := range 10 {
			req := &schedulerv1.ScheduleJobRequest{
				Name: strconv.Itoa(i),
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
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
		}

		for i := range 10 {
			req := &schedulerv1.ScheduleJobRequest{
				Name: strconv.Itoa(i),
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid2",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			}
			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)
		}

		resp, err := client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), 10)

		_, err = client.DeleteByMetadata(ctx, &schedulerv1.DeleteByMetadataRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GetJobs())

		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid2",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), 10)

		_, err = client.DeleteByMetadata(ctx, &schedulerv1.DeleteByMetadataRequest{
			IdPrefixMatch: ptr.Of(true),
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid2",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: new(schedulerv1.JobTargetMetadata_Job),
				},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GetJobs())
	})

	t.Run("actors", func(t *testing.T) {
		for i := range 10 {
			req := &schedulerv1.ScheduleJobRequest{
				Name: strconv.Itoa(i),
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: &schedulerv1.JobTargetMetadata_Actor{
							Actor: &schedulerv1.TargetActorReminder{
								Id:   "actorid",
								Type: "actortype",
							},
						},
					},
				},
			}
			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)
		}

		for i := range 10 {
			req := &schedulerv1.ScheduleJobRequest{
				Name: strconv.Itoa(i),
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: &schedulerv1.JobTargetMetadata_Actor{
							Actor: &schedulerv1.TargetActorReminder{
								Id:   "actorid2",
								Type: "actortype",
							},
						},
					},
				},
			}
			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)
		}

		resp, err := client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), 10)

		_, err = client.DeleteByMetadata(ctx, &schedulerv1.DeleteByMetadataRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GetJobs())

		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid2",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), 10)

		_, err = client.DeleteByMetadata(ctx, &schedulerv1.DeleteByMetadataRequest{
			IdPrefixMatch: ptr.Of(true),
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)

		resp, err = client.ListJobs(ctx, &schedulerv1.ListJobsRequest{
			Metadata: &schedulerv1.JobMetadata{
				AppId:     "appid",
				Namespace: "namespace",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Id:   "actorid2",
							Type: "actortype",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GetJobs())
	})
}
